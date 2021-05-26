package sync

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	. "github.com/Q42Philips/gitops-sync/pkg/config"
	"github.com/Q42Philips/gitops-sync/pkg/githubutil"
	"github.com/Q42Philips/gitops-sync/pkg/gitlogic"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/google/go-github/v33/github"
	"github.com/koron-go/prefixw"
	"github.com/pkg/errors"
)

var outputRepo *git.Repository

type Result struct {
	Commit     plumbing.Hash
	Repository *git.Repository
}

func Main(Global Config) (result Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			var isErr bool
			if err, isErr = r.(error); !isErr {
				err = fmt.Errorf("%s", fmt.Sprint(r))
			}
		}
	}()

	client, gitAuth := Global.GetClientAuth()
	ctx := context.Background()

	// Test auth
	u, _, err := client.Users.Get(ctx, "")
	if err != nil {
		log.Panic(err)
	} else {
		log.Printf("Signed in as %q", u.GetLogin())
		log.Println()
	}

	headRefName := plumbing.NewBranchReferenceName(Global.OutputHead)
	baseRefName := plumbing.NewBranchReferenceName(Global.OutputBase)
	orgName, repoName, err := githubutil.ParseGitHubRepo(Global.OutputRepoURL)
	orPanic(err, "parsing url")

	// Prepare output repository
	outputStorer := memory.NewStorage()
	outputFs := memfs.New()
	log.Printf("Cloning %s", maskURL(Global.OutputRepoURL))
	outputRepo, err = git.Clone(outputStorer, outputFs, &git.CloneOptions{
		Auth:     gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
		URL:      Global.OutputRepoURL,
		Depth:    Global.Depth,
	})
	result.Repository = *outputRepo
	orPanic(err, "cloning")
	log.Println()

	log.Println("Fetching all refs")
	err = outputRepo.Fetch(&git.FetchOptions{
		Auth:     gitAuth,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{"refs/*:refs/*"},
		Depth:    Global.Depth,
	})
	orPanic(err, "fetching (refs/*:refs/*)")
	log.Println()

	// Prepare begin state
	inputFs := osfs.New(Global.InputPath)
	w, err := outputRepo.Worktree()
	orPanic(err, "worktree")

	var startRef *plumbing.Reference
	startRef, err = outputRepo.Reference(baseRefName, true)
	orPanic(err, fmt.Sprintf("base branch %q does not exist, check your inputs", Global.OutputBase))

	log.Printf("Updating HEAD (%s)", Global.OutputHead)
	headRef, err := outputRepo.Reference(headRefName, true)
	var beforeRefspecs []config.RefSpec = nil
	if err == nil {
		// Reuse existing head branch
		log.Printf("Using %s as existing head", headRefName)
		// Store current head for safe push
		beforeRefspecs = []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", headRef.Hash(), headRefName))}
		if headRef.Hash() != startRef.Hash() {
			// Rebase existing head branch onto sync base by checking out the sync base before doing the sync again
			log.Printf("Rebasing %s onto %s (commit %s), discarding commit %s", headRef.Name().Short(), startRef.Name().Short(), startRef.Hash(), headRef.Hash())
		}
		err = w.Checkout(&git.CheckoutOptions{Hash: startRef.Hash(), Force: true})
		orPanic(err, fmt.Sprintf("worktree checkout to %s", startRef.Hash()))
	} else if err == plumbing.ErrReferenceNotFound {
		// Create new head branch
		log.Printf("Creating head branch %s from base %s", headRefName, baseRefName)
		err = w.Checkout(&git.CheckoutOptions{
			Branch: headRefName,
			Hash:   startRef.Hash(),
			Create: true,
		})
		orPanic(err, fmt.Sprintf("worktree checkout to %s := %s", headRefName, startRef.Hash()))
	} else {
		orPanic(err, "worktree checkout failed")
	}
	log.Println()

	// Commit options
	signature := &object.Signature{
		Name:  u.GetLogin(),
		Email: firstStr(u.GetEmail(), fmt.Sprintf("%s@users.noreply.github.com", u.GetLogin())),
		When:  time.Time(Global.CommitTime),
	}
	commitOpt := &git.CommitOptions{Author: signature, Committer: signature}

	// Do sync & commit
	obj := gitlogic.Sync(outputRepo, Global.OutputRepoPath, inputFs, commitOpt, Global.CommitMsg)
	log.Println()

	// Update reference
	ref := plumbing.NewHashReference(headRefName, obj.Hash)
	log.Printf("Setting ref %q to %s", ref.Name(), obj.Hash)
	err = outputStorer.SetReference(ref)
	orPanic(err, "creating ref")

	if Global.DryRun {
		log.Println("Stopping now because of dry-run")
		return
	}

	// Push
	refspec := config.RefSpec(fmt.Sprintf("%s:%s", headRefName, headRefName))
	log.Printf("$ git push %s --force-with-lease\n  leases: %s", refspec, beforeRefspecs)
	err = outputRepo.Push(&git.PushOptions{
		RefSpecs:          []config.RefSpec{refspec},
		RequireRemoteRefs: beforeRefspecs,
		Force:             true,
		Auth:              gitAuth,
		Progress:          prefixw.New(os.Stderr, "> "),
	})
	if err == git.NoErrAlreadyUpToDate {
		log.Println("Nothing to push, already up to date")
		err = nil
	}
	orPanic(err, "pushing")
	c, _, err := client.Repositories.GetCommit(ctx, orgName, repoName, obj.Hash.String())
	orPanic(err, "getting sync commit")
	defer func() { log.Printf("Browse %s %q", c.GetHTMLURL(), c.Commit.GetMessage()) }()
	log.Println()

	// Merge if requested
	if Global.BaseMerge != "" {
		log.Printf("Updating BASE (%s)", Global.BaseMerge)
		// Possibly skip making merge if it is a no-op
		baseMergeRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", Global.BaseMerge))
		baseMergeRef, err := outputRepo.Reference(baseMergeRefName, true)
		orPanic(err, "fetching merge base ref")
		baseMergeBeforeHash := baseMergeRef.Hash()
		if baseMergeBeforeHash == obj.Hash {
			log.Println("Skipping merge, already up to date")
			return Result{Commit: obj.Hash}, nil
		}

		// We merge by taking "--theirs" (to prevent issues where re-syncs don't overwrite because the commit already is in upstream)
		log.Printf("Merging %s into %s...", headRefName.Short(), Global.BaseMerge)

		// First checkout "ours" (the merge base)
		w.Checkout(&git.CheckoutOptions{Hash: baseMergeRef.Hash(), Force: true})
		orPanic(err, fmt.Sprintf("worktree checkout to merge base %s (%s)", baseMergeRef.Name().Short(), baseMergeRef.Hash().String()))

		// Draft merge commit opts
		commitOpt.Parents = []plumbing.Hash{baseMergeRef.Hash(), obj.Hash}
		commitOpt.Author.When = time.Now() // use current time
		commitOpt.Committer.When = commitOpt.Author.When
		// Then sync again by overwriting with our inputFs
		mergeCommit := gitlogic.Sync(outputRepo, Global.OutputRepoPath, inputFs, commitOpt, fmt.Sprintf("Merge %s into %s", headRefName.Short(), baseMergeRefName.Short()))

		// Update ref
		ref := plumbing.NewHashReference(baseMergeRefName, mergeCommit.Hash)
		err = outputStorer.SetReference(ref)
		orPanic(err, "updating ref (merged)")

		// Push
		refspec := config.RefSpec(fmt.Sprintf("%s:%s", baseMergeRefName, baseMergeRefName))
		beforeRefspecs := []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", baseMergeBeforeHash, baseMergeRefName))}
		log.Printf("$ git push %s --force-with-lease\n  leases: %s", refspec, beforeRefspecs)
		err = outputRepo.Push(&git.PushOptions{
			RefSpecs:          []config.RefSpec{refspec},
			RequireRemoteRefs: beforeRefspecs,
			Force:             true,
			Auth:              gitAuth,
			Progress:          prefixw.New(os.Stderr, "> "),
		})
		if err == git.NoErrAlreadyUpToDate {
			err = nil
		}
		orPanic(err, "pushing")
		c, _, err := client.Repositories.GetCommit(ctx, orgName, repoName, mergeCommit.Hash.String())
		orPanic(err, "getting custom merge commit")
		defer func() { log.Printf("Browse %s %q", c.GetHTMLURL(), c.Commit.GetMessage()) }()
	}

	// Pull Request if requested
	if Global.BasePR != "" {
		prs, _, err := client.PullRequests.List(ctx, orgName, repoName, &github.PullRequestListOptions{
			Head:  fmt.Sprintf("%s:%s", orgName, headRefName.Short()),
			Base:  Global.BasePR,
			State: "open",
		})
		orPanic(err, "getting existing prs")
		if len(prs) > 0 {
			log.Println("Existing PRs:")
			for _, pr := range prs {
				log.Println("-", pr.GetHTMLURL())
			}
			return Result{Commit: obj.Hash}, nil
		}

		// Possibly skip making PR if it is a no-op
		basePRRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", Global.BasePR))
		basePRRef, err := outputRepo.Reference(basePRRefName, true)
		orPanic(err, "fetching pr base ref")
		basePRBeforeHash := basePRRef.Hash()
		if basePRBeforeHash == obj.Hash {
			log.Println("Skipping pr, already up to date")
			return Result{Commit: obj.Hash}, nil
		}

		prTemplate := github.NewPullRequest{
			Head:  refStr(headRefName.Short()),
			Base:  &Global.BasePR,
			Draft: refBool(true),
			Body:  &Global.PrBody,
			Title: refStr(firstStr(Global.PrTitle, Global.CommitMsg)),
		}
		pr, _, err := client.PullRequests.Create(ctx, orgName, repoName, &prTemplate)
		orPanic(err, "creating pr")
		defer func() { log.Printf("Browse %s", pr.GetHTMLURL()) }()
	}
	log.Println()
	return Result{Commit: obj.Hash}, nil
}

func orPanic(err error, ctx string) {
	if err != nil {
		log.Panic(errors.Wrap(err, ctx))
	}
}

func maskURL(u string) string {
	parsed, err := url.Parse(u)
	orPanic(err, "url parsing")
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

// BackoffRetried tries a function 3 times and backs off while retrying
func BackoffRetried(fn func() error) (err error) {
	remaining := 3
	backoff := time.Millisecond * 100
	for {
		// try
		err = fn()
		if err == nil {
			return nil
		}

		// abort after retries
		remaining--
		if remaining < 0 {
			break
		}

		// retry after sleeping
		time.Sleep(backoff)
		backoff = backoff * 2
	}
	return err
}
