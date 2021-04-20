package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/jnovack/flag"
	"github.com/koron-go/prefixw"
	"github.com/pkg/errors"
)

var (
	commitMsg      = flag.String("message", "", "commit message, defaults to 'Sync ${CI_PROJECT_NAME:-$PWD}/$CI_COMMIT_REF_NAME to $OUTPUT_REPO_BRANCH")
	inputPath      = flag.String("input-path", ".", "where to read artifacts from")
	inputIgnore    = flag.String("input-ignore", "", "which files to read and which to skip (format is .gitignore format)")
	outputRepo     = flag.String("output-repo", "", "where to write artifacts to")
	outputRepoPath = flag.String("output-repo-path", ".", "where to write artifacts to")
	outputBase     = flag.String("output-base", "develop", "reference to use as basis")
	outputHead     = flag.String("output-head", "", "reference to write to & create a PR from into base; default = generated")
	// Either use
	authUsername = flag.String("github-username", "", "GitHub username to use for basic auth")
	authPassword = flag.String("github-password", "", "GitHub password to use for basic auth")
	authOtp      = flag.String("github-otp", "", "GitHub OTP to use for basic auth")
	// Or use
	authToken = flag.String("github-token", "", "GitHub token, authorize using env $GITHUB_TOKEN (convention)")
)

func init() {
	flag.Parse()
	log.SetFlags(0)

	if *outputRepo == "" {
		log.Fatal("No output repository set")
	}
}

func main() {
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
		*outputHead = fmt.Sprintf("sync/%s", time.Now().Format("20060102T150405Z"))
	}
	headRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", *outputHead))
	baseRefName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", *outputBase))

	// Prepare output repository
	outputStorer := memory.NewStorage()
	outputFs := memfs.New()
	log.Printf("Cloning %s (%s)", maskURL(*outputRepo), baseRefName)
	outputRepo, err := git.Clone(outputStorer, outputFs, &git.CloneOptions{
		Auth:          gitAuth,
		Progress:      prefixw.New(os.Stderr, "> "),
		URL:           *outputRepo,
		ReferenceName: baseRefName,
		SingleBranch:  true,
		Depth:         1,
	})
	orFatal(err, "cloning")
	log.Println()

	log.Printf("Fetching %s", headRefName)
	err = outputRepo.Fetch(&git.FetchOptions{
		Auth:     gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
		RefSpecs: []config.RefSpec{config.RefSpec(fmt.Sprintf("%s:%s", headRefName, headRefName))},
		Depth:    1,
	})
	if err == git.NoErrAlreadyUpToDate || errors.Is(err, git.NoMatchingRefSpecError{}) {
		err = nil
	}
	orFatal(err, "fetching pre-existing head")
	log.Println()

	// Prepare begin state
	inputFs := osfs.New(*inputPath)
	w, err := outputRepo.Worktree()
	orFatal(err, "worktree")

	var startRef *plumbing.Reference
	startRef, err = outputRepo.Reference(baseRefName, true)
	orFatal(err, fmt.Sprintf("base branch %q does not exist, check your inputs", *outputBase))

	_, err = outputRepo.Reference(headRefName, true)
	if err == nil {
		// Reuse existing head branch
		log.Printf("Using %s as existing head", headRefName)
		err = w.Checkout(&git.CheckoutOptions{
			Branch: headRefName,
			Create: false,
		})
		orFatal(err, "worktree checkout base branch")
	} else if err == plumbing.ErrReferenceNotFound {
		// Create new head branch
		log.Printf("Creating head branch %s from base %s", headRefName, baseRefName)
		err = w.Checkout(&git.CheckoutOptions{
			Branch: headRefName,
			Hash:   startRef.Hash(),
			Create: true,
		})
		orFatal(err, "worktree checkout head branch")
	} else {
		orFatal(err, "worktree checkout failed")
	}

	// Do sync
	log.Println()
	log.Println("Sync changes:")
	err = w.RemoveGlob(*outputRepoPath)
	orFatal(err, "removing old artifacts")
	if *outputRepoPath != "." && *outputRepoPath != "" {
		outputFs, err = chrootMkdir(outputFs, *outputRepoPath)
		orFatal(err, "failed to go to subdirectory")
	}
	err = copy(inputFs, outputFs)
	orFatal(err, "copy files")
	w.Add(*outputRepoPath)

	// Commit
	if *commitMsg == "" {
		project := os.Getenv("CI_PROJECT_NAME")
		if project == "" {
			project, _ = os.Getwd()
		}
		refName := os.Getenv("CI_COMMIT_REF_NAME")
		if refName == "" {
			refName = "unknown"
		}
		*commitMsg = fmt.Sprintf("Sync %s/%s to %s", project, refName, headRefName.Short())
	}
	status, err := w.Status()
	orFatal(err, "status")
	prefixw.New(log.Writer(), "> ").Write([]byte(status.String()))
	hash, err := w.Commit(*commitMsg, &git.CommitOptions{})
	orFatal(err, "committing")
	log.Println("Created commit", hash.String())
	obj, err := outputRepo.CommitObject(hash)
	orFatal(err, "committing")
	ref := plumbing.NewHashReference(headRefName, obj.Hash)
	err = outputStorer.SetReference(ref)
	orFatal(err, "creating ref")

	// Push
	refspec := config.RefSpec(fmt.Sprintf("%s:%s", ref.Name(), headRefName))
	log.Printf("Pushing %s", refspec)
	err = outputRepo.Push(&git.PushOptions{
		RefSpecs: []config.RefSpec{refspec},
		Auth:     gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
		Force:    true,
	})
	orFatal(err, "pushing")
	log.Println()
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
