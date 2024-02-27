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
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/google/go-github/v33/github"
	"github.com/koron-go/prefixw"
	"github.com/pkg/errors"
)

type Result struct {
	Commit     *object.Commit
	Repository *git.Repository
	PR         *github.PullRequest
}

type State struct {
	Global   Config
	orgName  string
	repoName string

	user    *github.User
	client  *github.Client
	gitAuth http.AuthMethod

	inputFs billy.Filesystem

	outputRepo *git.Repository
	worktree   *git.Worktree
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

	state := State{}
	if err = state.fromConfig(Global); err != nil {
		return result, errors.Wrap(err, "prepare")
	}

	// Sync
	result, err = state.syncBranch()
	if err != nil {
		return result, errors.Wrap(err, "sync branch")
	}
	htmlUrl := fmt.Sprintf("https://github.com/%s/%s/commit/%s", state.orgName, state.repoName, result.Commit.Hash)
	defer func() { log.Printf("Browse %s %q", htmlUrl, result.Commit.Message) }()

	// Auto-merge some syncs
	mergeResult, err := state.merge(result.Commit)
	if err != nil {
		return result, errors.Wrap(err, "sync merge")
	}
	defer func() { log.Printf("Browse %s %q", htmlUrl, mergeResult.Commit.Message) }()

	// Create PR for the other syncs
	result, err = state.pr(result.Commit)
	if err != nil {
		return result, errors.Wrap(err, "sync pull request")
	}
	if result.PR != nil {
		defer func() { log.Printf("Browse %s", result.PR.GetHTMLURL()) }()
	}

	return
}

func (state *State) fromConfig(Global Config) (error) {
	var err error
	state.orgName, state.repoName, err = githubutil.ParseGitHubRepo(Global.OutputRepoURL)
	if err != nil {
		return errors.Wrap(err, "parsing output repo url")
	}

	state.Global = Global
	ctx := context.Background()
	state.client, state.gitAuth, err = Global.GetClientAuth()
	if err != nil {
		log.Panic(err)
	}

	// Test auth
	state.user, _, err = state.client.Users.Get(ctx, "")
	if err != nil {
		log.Panic(err)
	} else {
		log.Printf("Signed in as %q", state.user.GetLogin())
		log.Println()
	}

	// Prepare output repository
	outputStorer := memory.NewStorage()
	outputFs := memfs.New()
	u, err := maskURL(Global.OutputRepoURL)
	if err != nil {
		return errors.Wrap(err, "masking url")
	}
	log.Printf("Cloning %s", u)
	state.outputRepo, err = git.Clone(outputStorer, outputFs, &git.CloneOptions{
		Auth:     state.gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
		URL:      Global.OutputRepoURL,
		Depth:    Global.Depth,
	})
	if err != nil {
		return errors.Wrap(err, "cloning")
	}

	log.Println()

	log.Println("Fetching all refs")
	err = state.outputRepo.Fetch(&git.FetchOptions{
		Auth:     state.gitAuth,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{"refs/*:refs/*"},
		Depth:    Global.Depth,
	})
	if err != nil {
		return errors.Wrap(err, "fetching (refs/*:refs/*)")
	}
	log.Println()

	// Prepare begin state
	state.inputFs = osfs.New(Global.InputPath)
	state.worktree, err = state.outputRepo.Worktree()
	if err != nil {
		return errors.Wrap(err, "worktree")
	}
	return err
}

func (state State) syncBranch() (Result, error) {
	Global := state.Global
	headRefName := plumbing.NewBranchReferenceName(Global.OutputHead)
	baseRefName := plumbing.NewBranchReferenceName(Global.OutputBase)
	result := Result{}

	startRef, err := state.outputRepo.Reference(baseRefName, true)
	if err != nil {
		return result, errors.Wrapf(err,"base branch %q does not exist, check your inputs", Global.OutputBase)
	}

	log.Printf("Updating HEAD (%s)", Global.OutputHead)
	beforeRefspecs := make([]config.RefSpec, 0)
	headRef, err := state.outputRepo.Reference(headRefName, true)
	if err == nil {
		// Reuse existing head branch
		log.Printf("Using %s as existing head", headRefName)
		// Store current head for safe push
		beforeRefspecs = []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", headRef.Hash(), headRefName))}
		if headRef.Hash() != startRef.Hash() {
			// Rebase existing head branch onto sync base by checking out the sync base before doing the sync again
			log.Printf("Rebasing %s onto %s (commit %s), discarding commit %s", headRef.Name().Short(), startRef.Name().Short(), startRef.Hash(), headRef.Hash())
		}
		err = state.worktree.Checkout(&git.CheckoutOptions{Hash: startRef.Hash(), Force: true})
		if err != nil {
			return result, errors.Wrapf(err, "worktree checkout to %s", startRef.Hash())
		}
	} else if errors.Is(err,plumbing.ErrReferenceNotFound) {
		// Create new head branch
		log.Printf("Creating head branch %s from base %s", headRefName, baseRefName)
		err = state.worktree.Checkout(&git.CheckoutOptions{
			Branch: headRefName,
			Hash:   startRef.Hash(),
			Create: true,
		})
		if err != nil {
			return result, errors.Wrapf(err, "worktree checkout to %s := %s", headRefName, startRef.Hash())
		}
	} else {
		return result, errors.Wrap(err, "fetching head ref")
	}
	log.Println()

	// Commit options
	signature := &object.Signature{
		Name:  state.user.GetLogin(),
		Email: firstStr(state.user.GetEmail(), fmt.Sprintf("%s@users.noreply.github.com", state.user.GetLogin())),
		When:  time.Time(Global.CommitTime),
	}
	commitOpt := &git.CommitOptions{Author: signature, Committer: signature}

	// Do sync & commit
	obj, err := gitlogic.Sync(state.outputRepo, Global.OutputRepoPath(), state.inputFs, commitOpt, Global.CommitMsg)
	if err != nil {
		return result, errors.Wrap(err, "syncing")
	}

	result = Result{Commit: obj, Repository: state.outputRepo}
	log.Println()

	// Update reference
	ref := plumbing.NewHashReference(headRefName, obj.Hash)
	log.Printf("Setting ref %q to %s", ref.Name(), obj.Hash)
	err = state.outputRepo.Storer.SetReference(ref)
	if err != nil {
		return result, errors.Wrap(err, "setting ref")
	}

	if Global.DryRun {
		log.Println("Stopping now because of dry-run")
		return result, nil
	}

	// Push
	refspec := config.RefSpec(fmt.Sprintf("%s:%s", obj.Hash, headRefName))
	log.Printf("$ git push %s --force-with-lease\n  leases: %s", refspec, beforeRefspecs)
	err = state.outputRepo.Push(&git.PushOptions{
		RefSpecs:          []config.RefSpec{refspec},
		RequireRemoteRefs: beforeRefspecs,
		Force:             true,
		Auth:              state.gitAuth,
		Progress:          prefixw.New(os.Stderr, "> "),
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		log.Println("Nothing to push, already up to date")
		err = nil
	}
	if err != nil {
		// Recover untyped error "remote ref refs/heads/... required to be ... but is ..." with refetch
		_ = state.outputRepo.Fetch(&git.FetchOptions{
			Auth:     state.gitAuth,
			Progress: os.Stdout,
			RefSpecs: []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", headRefName, headRefName))},
			Depth:    1,
		})
		recheckedHeadRef, _ := state.outputRepo.Reference(headRefName, true)
		if recheckedHeadRef != nil && recheckedHeadRef.Hash() == ref.Hash() {
			log.Println("Updated in parallel sync, already up to date")
			err = nil
		}
	}
	return result, err
}

func (state State) merge(obj *object.Commit) (Result, error) {
	Global := state.Global
	headRefName := plumbing.NewBranchReferenceName(Global.OutputHead)
	result := Result{}

	// Merge if requested
	if Global.BaseMerge == "" {
		return result, nil
	}

	log.Printf("Updating BASE (%s)", Global.BaseMerge)
	// Possibly skip making merge if it is a no-op
	baseMergeRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", Global.BaseMerge))
	baseMergeRef, err := state.outputRepo.Reference(baseMergeRefName, true)
	if err != nil {
		return result, errors.Wrap(err, "fetching merge base ref")
	}

	baseMergeBeforeHash := baseMergeRef.Hash()
	if baseMergeBeforeHash == obj.Hash {
		log.Println("Skipping merge, already up to date")
		return result, nil
	}

	// We merge by taking "--theirs" (to prevent issues where re-syncs don't overwrite because the commit already is in upstream)
	log.Printf("Merging %s into %s...", headRefName.Short(), Global.BaseMerge)

	// First checkout "ours" (the merge base)
	err = state.worktree.Checkout(&git.CheckoutOptions{Hash: baseMergeRef.Hash(), Force: true})
	if err != nil {
		return result, errors.Wrapf(err, "worktree checkout to merge base %s (%s)", baseMergeRef.Name().Short(), baseMergeRef.Hash().String())
	}

	// Draft merge commit opts
	signature := &object.Signature{
		Name:  state.user.GetLogin(),
		Email: firstStr(state.user.GetEmail(), fmt.Sprintf("%s@users.noreply.github.com", state.user.GetLogin())),
		When:  time.Now(), // use current time
	}
	commitOpt := &git.CommitOptions{
		Parents:   []plumbing.Hash{baseMergeRef.Hash(), obj.Hash},
		Author:    signature,
		Committer: signature,
	}
	// Then sync again by overwriting with our inputFs
	mergeCommit, err := gitlogic.Sync(state.outputRepo, Global.OutputRepoPath(), state.inputFs, commitOpt, fmt.Sprintf("Merge %s into %s", headRefName.Short(), baseMergeRefName.Short()))
	if err != nil {
		return result, errors.Wrap(err, "syncing for merge")
	}

	result.Commit = mergeCommit // update object to wait for

	// Push
	refspec := config.RefSpec(fmt.Sprintf("%s:%s", mergeCommit.Hash, baseMergeRefName))
	beforeRefspecs := []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", baseMergeBeforeHash, baseMergeRefName))}
	log.Printf("$ git push %s --force-with-lease\n  leases: %s", refspec, beforeRefspecs)
	err = state.outputRepo.Push(&git.PushOptions{
		RefSpecs:          []config.RefSpec{refspec},
		RequireRemoteRefs: beforeRefspecs,
		Force:             true,
		Auth:              state.gitAuth,
		Progress:          prefixw.New(os.Stderr, "> "),
	})
	if err != nil {
		if !errors.Is(err, git.NoErrAlreadyUpToDate) {
			return result, errors.Wrap(err, "pushing merge")
		}
	}
	result.Commit = mergeCommit
	return result, nil
}

func (state State) pr(obj *object.Commit) (Result, error) {
	ctx := context.Background()
	Global := state.Global
	headRefName := plumbing.NewBranchReferenceName(Global.OutputHead)
	result := Result{}

	// Pull Request if requested
	if Global.BasePR == "" {
		return result, nil
	}
	prs, _, err := state.client.PullRequests.List(ctx, state.orgName, state.repoName, &github.PullRequestListOptions{
		Head:  fmt.Sprintf("%s:%s", state.orgName, headRefName.Short()),
		Base:  Global.BasePR,
		State: "open",
	})
	if err != nil {
		return result, errors.Wrap(err, "getting existing prs")
	}
	if len(prs) > 0 {
		log.Println("Existing PRs:")
		for _, pr := range prs {
			log.Println("-", pr.GetHTMLURL())
			result.PR = pr
		}
		return result, nil
	}

	// Possibly skip making PR if it is a no-op
	basePRRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", Global.BasePR))
	basePRRef, err := state.outputRepo.Reference(basePRRefName, true)
	if err != nil {
		return result, errors.Wrap(err, "fetching pr base ref")
	}

	basePRBeforeHash := basePRRef.Hash()
	if basePRBeforeHash == obj.Hash {
		log.Println("Skipping pr, already up to date")
		return result, nil
	}

	prTemplate := github.NewPullRequest{
		Head:  refStr(headRefName.Short()),
		Base:  &Global.BasePR,
		Draft: refBool(true),
		Body:  &Global.PrBody,
		Title: refStr(firstStr(Global.PrTitle, Global.CommitMsg)),
	}
	result.PR, _, err = state.client.PullRequests.Create(ctx, state.orgName, state.repoName, &prTemplate)
	if err != nil {
		return result, err
	}

	return result, nil
}

// maskURL replaces the password in a URL with "masked"
func maskURL(u string) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return u, errors.Wrap(err, "parsing url")
	}
	if parsed.User == nil {
		return u, nil
	}
	info := url.User(parsed.User.Username())
	if _, hasPwd := parsed.User.Password(); hasPwd {
		info = url.UserPassword(parsed.User.Username(), "masked")
	}
	parsed.User = info
	return parsed.String(), nil
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
