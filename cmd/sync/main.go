package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"

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
	commitMsg        = flag.String("message", "", "commit message, defaults to 'Sync ${CI_PROJECT_NAME:-$PWD}/$CI_COMMIT_REF_NAME to $OUTPUT_REPO_BRANCH")
	inputPath        = flag.String("input-path", ".", "where to read artifacts from")
	inputIgnore      = flag.String("input-ignore", "", "which files to read and which to skip (format is .gitignore format)")
	outputRepo       = flag.String("output-repo", "", "where to write artifacts to")
	outputRepoPath   = flag.String("output-repo-path", ".", "where to write artifacts to")
	outputRepoBranch = flag.String("output-repo-branch", "develop", "where to write artifacts to")
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
	}

	// Prepare output
	outputStorer := memory.NewStorage()
	outputFs := memfs.New()
	log.Printf("Cloning %s", maskURL(*outputRepo))
	outputRepo, err := git.Clone(outputStorer, outputFs, &git.CloneOptions{
		URL:      *outputRepo,
		Auth:     gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
	})
	orFatal(err, "cloning")

	// Gather inputs
	inputFs := osfs.New(*inputPath)

	// Do sync
	w, err := outputRepo.Worktree()
	orFatal(err, "worktree")
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
		*commitMsg = fmt.Sprintf("Sync %s/%s to %s", project, refName, *outputRepoBranch)
	}
	status, err := w.Status()
	orFatal(err, "status")
	prefixw.New(log.Writer(), "> ").Write([]byte(status.String()))
	hash, err := w.Commit(*commitMsg, &git.CommitOptions{})
	orFatal(err, "committing")
	log.Println("Created commit", hash.String())
	obj, err := outputRepo.CommitObject(hash)
	orFatal(err, "committing")
	ref := plumbing.NewHashReference("refs/heads/tmp", obj.Hash)
	err = outputStorer.SetReference(ref)
	orFatal(err, "creating ref")

	// Push
	refspec := config.RefSpec(fmt.Sprintf("%s:refs/heads/%s", "refs/heads/tmp", *outputRepoBranch))
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
