package sync

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Q42Philips/gitops-sync/pkg/gitlogic"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/google/go-github/v33/github"
	"github.com/jnovack/flag"
	"github.com/koron-go/prefixw"
	"github.com/pkg/errors"
)

// flags
var (
	commitMsg      = flag.String("message", "", "commit message, defaults to 'Sync ${CI_PROJECT_NAME:-$PWD}/$CI_COMMIT_REF_NAME to $OUTPUT_REPO_BRANCH")
	inputPath      = flag.String("input-path", ".", "where to read artifacts from")
	outputRepoURL  = flag.String("output-repo", "", "where to write artifacts to")
	outputRepoPath = flag.String("output-repo-path", ".", "where to write artifacts to")
	outputBase     = flag.String("output-base", "develop", "reference to use as basis")
	outputHead     = flag.String("output-head", "", "reference to write to & create a PR from into base; default = generated")
	basePR         = flag.String("pr", "", "whether to create a PR, and if set, which branch to set as PR base")
	baseMerge      = flag.String("merge", "", "whether to merge straight away, which branch to set as merge base")
	prBody         = flag.String("pr-body", "Sync", "Body of PR")
	prTitle        = flag.String("pr-title", "Sync", "Title of PR; defaults to commit message")
	commitTime     = flag.String("commit-timestamp", "now", "Time of the commit; for example $CI_COMMIT_TIMESTAMP of the original commit")
	dryRun         = flag.Bool("dry-run", false, "Do not push, merge, nor PR")
	depth          = flag.Int("depth", 0, "Set the depth to do a shallow clone. Use with caution, go-git pushes can fail for shallow branches.")
	// Either use
	authUsername = flag.String("github-username", "", "GitHub username to use for basic auth")
	authPassword = flag.String("github-password", "", "GitHub password to use for basic auth")
	authOtp      = flag.String("github-otp", "", "GitHub OTP to use for basic auth")
	// Or use
	authToken = flag.String("github-token", "", "GitHub token, authorize using env $GITHUB_TOKEN (convention)")
)

func init() {
	flag.Parse()

	if *outputRepoURL == "" {
		log.Fatal("No output repository set")
	}
}

var outputRepo *git.Repository

func Main() {
	client, gitAuth := getClientAuth()
	ctx := context.Background()

	// Test auth
	u, _, err := client.Users.Get(ctx, "")
	if err != nil {
		log.Panic(err)
	} else {
		log.Printf("Signed in as %q", u.GetLogin())
		log.Println()
	}

	// Options
	if *outputHead == "" {
		*outputHead = fmt.Sprintf("auto/sync/%s", time.Now().Format("20060102T150405Z"))
	}
	headRefName := plumbing.NewBranchReferenceName(*outputHead)
	baseRefName := plumbing.NewBranchReferenceName(*outputBase)
	orgName, repoName, err := parseGitHubRepo(*outputRepoURL)
	orFatal(err, "parsing url")

	if *commitMsg == "" {
		project := os.Getenv("CI_PROJECT_NAME")
		if project == "" {
			project, _ = os.Getwd()
		}
		refName := os.Getenv("CI_COMMIT_REF_NAME")
		if refName == "" {
			refName = "unknown"
		}
		*commitMsg = fmt.Sprintf("Sync %s/%s", project, refName)
	}

	// Prepare output repository
	outputStorer := memory.NewStorage()
	outputFs := memfs.New()
	log.Printf("Cloning %s", maskURL(*outputRepoURL))
	outputRepo, err = git.Clone(outputStorer, outputFs, &git.CloneOptions{
		Auth:     gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
		URL:      *outputRepoURL,
		Depth:    *depth,
	})
	orFatal(err, "cloning")
	log.Println()

	log.Println("Fetching all refs")
	err = outputRepo.Fetch(&git.FetchOptions{
		Auth:     gitAuth,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{"refs/*:refs/*"},
		Depth:    *depth,
	})
	orFatal(err, "fetching (refs/*:refs/*)")

	// Prepare begin state
	inputFs := osfs.New(*inputPath)
	w, err := outputRepo.Worktree()
	orFatal(err, "worktree")

	var startRef *plumbing.Reference
	startRef, err = outputRepo.Reference(baseRefName, true)
	orFatal(err, fmt.Sprintf("base branch %q does not exist, check your inputs", *outputBase))

	headRef, err := outputRepo.Reference(headRefName, true)
	var beforeRefspecs []config.RefSpec = nil
	if err == nil {
		// Reuse existing head branch
		log.Printf("Using %s as existing head", headRefName)
		// Store current head for safe push
		beforeRefspecs = []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", headRef.Hash(), headRefName))}
		if headRef.Hash() != startRef.Hash() {
			// Rebase existing head branch onto sync base by checking out the sync base before doing the sync again
			log.Printf("Rebasing %s to %s, discarding commit %s", headRefName, startRef.Hash(), headRef.Hash())
		}
		err = w.Checkout(&git.CheckoutOptions{Hash: startRef.Hash(), Force: true})
		orFatal(err, fmt.Sprintf("worktree checkout to %s", startRef.Hash()))
	} else if err == plumbing.ErrReferenceNotFound {
		// Create new head branch
		log.Printf("Creating head branch %s from base %s", headRefName, baseRefName)
		err = w.Checkout(&git.CheckoutOptions{
			Branch: headRefName,
			Hash:   startRef.Hash(),
			Create: true,
		})
		orFatal(err, fmt.Sprintf("worktree checkout to %s := %s", headRefName, startRef.Hash()))
	} else {
		orFatal(err, "worktree checkout failed")
	}
	log.Println()

	// Commit options
	var t time.Time = time.Now()
	if *commitTime != "now" {
		// Allow a configured commit time to allow aligning GitOps commits to the original repo commit
		t, err = time.Parse(time.RFC3339, *commitTime)
		orFatal(err, "parsing commit time with RFC3339/ISO8601 format")
	}
	signature := &object.Signature{
		Name:  u.GetLogin(),
		Email: firstStr(u.GetEmail(), fmt.Sprintf("%s@users.noreply.github.com", u.GetLogin())),
		When:  t,
	}
	commitOpt := &git.CommitOptions{Author: signature, Committer: signature}

	// Do sync & commit
	obj := gitlogic.Sync(outputRepo, *outputRepoPath, inputFs, commitOpt, *commitMsg)

	// Commit
	ref := plumbing.NewHashReference(headRefName, obj.Hash)
	err = outputStorer.SetReference(ref)
	orFatal(err, "creating ref")

	if *dryRun {
		log.Println("Stopping now because of dry-run")
		return
	}

	// Push
	refspec := config.RefSpec(fmt.Sprintf("%s:%s", obj.Hash, headRefName))
	log.Printf("Pushing %s with before-state %s", refspec, beforeRefspecs)
	err = outputRepo.Push(&git.PushOptions{
		RefSpecs:          []config.RefSpec{refspec},
		RequireRemoteRefs: beforeRefspecs,
		Auth:              gitAuth,
		Progress:          prefixw.New(os.Stderr, "> "),
	})
	if err == git.NoErrAlreadyUpToDate {
		log.Println("Nothing to push, already up to date")
		err = nil
	}
	orFatal(err, "pushing")
	log.Println()

	// Merge if requested
	if *baseMerge != "" {
		// Possibly skip making merge if it is a no-op
		baseMergeRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", *baseMerge))
		baseMergeRef, err := outputRepo.Reference(baseMergeRefName, true)
		orFatal(err, "fetching merge base ref")
		baseMergeBeforeHash := baseMergeRef.Hash()
		if baseMergeBeforeHash == obj.Hash {
			log.Println("Skipping merge, already up to date")
			return
		}

		// We merge by taking "--theirs" (to prevent issues where re-syncs don't overwrite because the commit already is in upstream)
		log.Printf("Merging %s into %s...", headRefName.Short(), *baseMerge)

		// First checkout "ours" (the merge base)
		w.Checkout(&git.CheckoutOptions{Hash: baseMergeRef.Hash(), Force: true})
		orFatal(err, fmt.Sprintf("worktree checkout to merge base %s (%s)", baseMergeRef.Name().Short(), baseMergeRef.Hash().String()))

		// Draft merge commit opts
		commitOpt.Parents = []plumbing.Hash{baseMergeRef.Hash(), obj.Hash}
		commitOpt.Author.When = time.Now() // use current time
		commitOpt.Committer.When = commitOpt.Author.When
		// Then sync again by overwriting with our inputFs
		mergeCommit := gitlogic.Sync(outputRepo, *outputRepoPath, inputFs, commitOpt, fmt.Sprintf("Merge %s into %s", headRefName.Short(), baseMergeRefName.Short()))

		// Update ref
		ref := plumbing.NewHashReference(baseMergeRefName, mergeCommit.Hash)
		err = outputStorer.SetReference(ref)
		orFatal(err, "updating ref (merged)")

		// Push
		refspec := config.RefSpec(fmt.Sprintf("%s:%s", baseMergeRefName, baseMergeRefName))
		beforeRefspec := config.RefSpec(fmt.Sprintf("%s:%s", baseMergeBeforeHash, baseMergeRefName))
		log.Printf("Pushing %s with before-state %s", refspec, beforeRefspec)
		err = outputRepo.Push(&git.PushOptions{
			RefSpecs:          []config.RefSpec{refspec},
			RequireRemoteRefs: []config.RefSpec{beforeRefspec},
			Auth:              gitAuth,
			Progress:          prefixw.New(os.Stderr, "> "),
		})
		if err == git.NoErrAlreadyUpToDate {
			err = nil
		}
		orFatal(err, "pushing")
		c, _, err := client.Repositories.GetCommit(ctx, orgName, repoName, obj.Hash.String())
		orFatal(err, "getting custom merge commit")
		log.Println(c.Commit.GetMessage(), c.GetHTMLURL())
		return
	}

	// Pull Request if requested
	if *basePR != "" {
		prs, _, err := client.PullRequests.List(ctx, orgName, repoName, &github.PullRequestListOptions{
			Head:  fmt.Sprintf("%s:%s", orgName, headRefName.Short()),
			Base:  *basePR,
			State: "open",
		})
		orFatal(err, "getting existing prs")
		if len(prs) > 0 {
			log.Println("Existing PRs:")
			for _, pr := range prs {
				log.Println("-", pr.GetHTMLURL())
			}
			return
		}

		// Possibly skip making PR if it is a no-op
		basePRRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", *basePR))
		basePRRef, err := outputRepo.Reference(basePRRefName, true)
		orFatal(err, "fetching pr base ref")
		basePRBeforeHash := basePRRef.Hash()
		if basePRBeforeHash == obj.Hash {
			log.Println("Skipping pr, already up to date")
			return
		}

		prTemplate := github.NewPullRequest{
			Head:  refStr(headRefName.Short()),
			Base:  basePR,
			Draft: refBool(true),
			Body:  prBody,
			Title: refStr(firstStr(*prTitle, *commitMsg)),
		}
		pr, _, err := client.PullRequests.Create(ctx, orgName, repoName, &prTemplate)
		orFatal(err, "creating pr")
		log.Println(pr.GetHTMLURL())
	}
}

func orFatal(err error, ctx string) {
	if err != nil {
		log.Fatal(errors.Wrap(err, ctx))
	}
}

func maskURL(u string) string {
	parsed, err := url.Parse(u)
	orFatal(err, "url parsing")
	if parsed.User == nil {
		return u
	}
	info := url.User(parsed.User.Username())
	if _, hasPwd := parsed.User.Password(); hasPwd {
		info = url.UserPassword(parsed.User.Username(), "masked")
	}
	parsed.User = info
	return parsed.String()
}

func parseGitHubRepo(u string) (org, repo string, err error) {
	p, err := url.Parse(u)
	if err != nil {
		return "", "", err
	}
	pathSegments := strings.Split(strings.Trim(strings.TrimRight(p.Path, ".git"), "/"), "/")
	if len(pathSegments) < 2 {
		return "", "", errors.New("invalid github url")
	}
	return pathSegments[0], pathSegments[1], nil
}

func refStr(inp string) *string {
	return &inp
}
func refBool(inp bool) *bool {
	return &inp
}

func firstStr(args ...string) string {
	for _, a := range args {
		if a != "" {
			return a
		}
	}
	return ""
}
