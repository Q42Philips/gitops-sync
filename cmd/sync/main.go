package sync

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
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
	outputRepo     = flag.String("output-repo", "", "where to write artifacts to")
	outputRepoPath = flag.String("output-repo-path", ".", "where to write artifacts to")
	outputBase     = flag.String("output-base", "develop", "reference to use as basis")
	outputHead     = flag.String("output-head", "", "reference to write to & create a PR from into base; default = generated")
	basePR         = flag.String("pr", "", "whether to create a PR, and if set, which branch to set as PR base")
	baseMerge      = flag.String("merge", "", "whether to merge straight away, which branch to set as merge base")
	prBody         = flag.String("pr-body", "Sync", "Body of PR")
	prTitle        = flag.String("pr-title", "Sync", "Title of PR; defaults to commit message")
	commitTime     = flag.String("commit-timestamp", "now", "Time of the commit; for example $CI_COMMIT_TIMESTAMP of the original commit")
	dryRun         = flag.Bool("dry-run", false, "Do not push, merge, nor PR")
	// Either use
	authUsername = flag.String("github-username", "", "GitHub username to use for basic auth")
	authPassword = flag.String("github-password", "", "GitHub password to use for basic auth")
	authOtp      = flag.String("github-otp", "", "GitHub OTP to use for basic auth")
	// Or use
	authToken = flag.String("github-token", "", "GitHub token, authorize using env $GITHUB_TOKEN (convention)")
)

func init() {
	flag.Parse()

	if *outputRepo == "" {
		log.Fatal("No output repository set")
	}
}

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
	orgName, repoName, err := parseGitHubRepo(*outputRepo)
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
	log.Printf("Cloning %s", maskURL(*outputRepo))
	outputRepo, err := git.Clone(outputStorer, outputFs, &git.CloneOptions{
		Auth:     gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
		URL:      *outputRepo,
		Depth:    10,
	})
	orFatal(err, "cloning")
	log.Println()

	log.Printf("Fetching all refs (%s)", baseRefName)
	err = outputRepo.Fetch(&git.FetchOptions{
		Auth:     gitAuth,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{"refs/*:refs/*"},
		Depth:    10,
	})
	orFatal(err, "fetching")

	// Prepare begin state
	inputFs := osfs.New(*inputPath)
	w, err := outputRepo.Worktree()
	orFatal(err, "worktree")

	var startRef *plumbing.Reference
	startRef, err = outputRepo.Reference(baseRefName, true)
	orFatal(err, fmt.Sprintf("base branch %q does not exist, check your inputs", *outputBase))

	headRef, err := outputRepo.Reference(headRefName, true)
	var beforeRefspecs []config.RefSpec = nil
	if err == nil && !headRef.Hash().IsZero() {
		// Reuse existing head branch
		log.Printf("Using %s as existing head", headRefName)
		err = w.Checkout(&git.CheckoutOptions{
			Branch: headRefName,
		})
		orFatal(err, fmt.Sprintf("worktree checkout to %s (%s)", headRefName, headRef.Hash()))
		// Store current head for safe push
		beforeRefspecs = []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", headRef.Hash(), headRefName))}
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
	obj := sync(outputRepo, inputFs, commitOpt, *commitMsg)

	// Commit
	ref := plumbing.NewHashReference(headRefName, obj.Hash)
	err = outputStorer.SetReference(ref)
	orFatal(err, "creating ref")

	if *dryRun {
		log.Println("Stopping now because of dry-run")
		return
	}

	// Push
	refspec := config.RefSpec(fmt.Sprintf("%s:%s", ref.Name(), headRefName))
	log.Printf("Pushing %s", refspec)
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

		log.Printf("Merging %s into %s...", headRefName.Short(), *baseMerge)
		c, _, err := client.Repositories.Merge(ctx, orgName, repoName, &github.RepositoryMergeRequest{
			Head: refStr(headRefName.Short()),
			Base: refStr(baseMergeRefName.Short()),
		})
		// Merge conflict, try to resolve by taking "--ours"
		if err != nil && strings.Contains(err.Error(), "409") {
			log.Printf("Merge conflict. Trying to resolve.")
			log.Printf("Fetching %s", baseMergeRefName.Short())
			err = outputRepo.Fetch(&git.FetchOptions{
				Auth:     gitAuth,
				Progress: prefixw.New(os.Stderr, "> "),
				RefSpecs: []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", baseMergeRefName, baseMergeRefName))},
				Depth:    1,
			})
			if err == git.NoErrAlreadyUpToDate {
				err = nil
			}
			orFatal(err, "fetching merge base")

			// First checkout theirs
			w.Checkout(&git.CheckoutOptions{
				Branch: baseMergeRefName,
				Force:  true,
			})
			orFatal(err, fmt.Sprintf("worktree checkout to %s (unknown)", baseMergeRefName))

			// Draft merge commit opts
			commitOpt.Parents = []plumbing.Hash{baseMergeBeforeHash, obj.Hash}
			// Then sync again by overwriting with our inputFs
			obj = sync(outputRepo, inputFs, commitOpt, fmt.Sprintf("Merge %s into %s", headRefName.Short(), baseMergeRefName.Short()))
			ref := plumbing.NewHashReference(baseMergeRefName, obj.Hash)
			err = outputStorer.SetReference(ref)
			orFatal(err, "updating ref (merged)")

			// Push
			refspec := config.RefSpec(fmt.Sprintf("%s:%s", baseMergeRefName, baseMergeRefName))
			beforeRefspec := config.RefSpec(fmt.Sprintf("%s:%s", baseMergeBeforeHash, baseMergeRefName))
			log.Printf("Pushing %s", refspec)
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
			c, _, err = client.Repositories.GetCommit(ctx, orgName, repoName, obj.Hash.String())
			orFatal(err, "getting custom merge commit")
		}
		orFatal(err, "merging")
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

func sync(gr *git.Repository, inputFs billy.Filesystem, commitOpt *git.CommitOptions, msg string) *object.Commit {
	// Do sync
	w, err := gr.Worktree()
	orFatal(err, "getting worktree")

	outputFs := w.Filesystem
	err = rmRecursively(outputFs, *outputRepoPath) // remove existing files
	orFatal(err, "removing old artifacts")
	if *outputRepoPath != "." && *outputRepoPath != "" {
		outputFs, err = chrootMkdir(outputFs, *outputRepoPath)
		orFatal(err, "failed to go to subdirectory")
	}
	err = copy(inputFs, outputFs)
	orFatal(err, "copy files")
	w.Add(*outputRepoPath)

	// Print status
	status, err := w.Status()
	if len(status) == 0 {
		log.Println("No changes. Skipping commit.")
		head, err := gr.Head()
		orFatal(err, "getting head")
		obj, err := gr.CommitObject(head.Hash())
		orFatal(err, "getting commit")
		return obj
	}

	log.Println("Sync changes:")
	orFatal(err, "status")
	prefixw.New(log.Writer(), "> ").Write([]byte(status.String()))

	// Commit
	w.Status()
	hash, err := w.Commit(msg, commitOpt)
	orFatal(err, "committing")
	log.Println("Created commit", hash.String())
	obj, err := gr.CommitObject(hash)
	orFatal(err, "getting commit")
	return obj
}
