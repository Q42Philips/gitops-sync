package config

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jnovack/flag"
)

func (c *Config) Init() {
	// flags
	flag.StringVar(&c.CommitMsg, "message", "", "commit message, defaults to 'Sync ${CI_PROJECT_NAME:-$PWD}/$CI_COMMIT_REF_NAME to $OUTPUT_REPO_BRANCH")
	flag.StringVar(&c.InputPath, "input-path", ".", "where to read artifacts from")
	flag.StringVar(&c.OutputRepoURL, "output-repo", "", "where to write artifacts to")
	flag.StringVar(&c.OutputRepoPathList, "output-repo-path", ".", "where to write artifacts to, comma separated list of paths in the repo")
	flag.StringVar(&c.OutputBase, "output-base", "develop", "reference to use as basis")
	flag.StringVar(&c.OutputHead, "output-head", "", "reference to write to & create a PR from into base; default = generated")
	flag.StringVar(&c.BasePR, "pr", "", "whether to create a PR, and if set, which branch to set as PR base")
	flag.StringVar(&c.BaseMerge, "merge", "", "whether to merge straight away, which branch to set as merge base")
	flag.StringVar(&c.PrBody, "pr-body", "Sync", "Body of PR")
	flag.StringVar(&c.PrTitle, "pr-title", "Sync", "Title of PR; defaults to commit message")
	flag.Var(&c.CommitTime, "commit-timestamp", "Time of the commit; for example $CI_COMMIT_TIMESTAMP of the original commit (default: now)")

	flag.BoolVar(&c.DryRun, "dry-run", false, "Do not push, merge, nor PR")
	flag.IntVar(&c.Depth, "depth", 0, "Set the depth to do a shallow clone. Use with caution, go-git pushes can fail for shallow branches.")

	// Wait for tags
	flag.Var(&c.WaitForTags, "wait-for-tags", "Wait for certain tags to update (glob patterns supported): example flux-sync or gke_myproject_*")

	// Authentication
	// Either use
	flag.StringVar(&c.AuthUsername, "github-username", "", "GitHub username to use for basic auth")
	flag.StringVar(&c.AuthPassword, "github-password", "", "GitHub password to use for basic auth")
	flag.StringVar(&c.AuthOtp, "github-otp", "", "GitHub OTP to use for basic auth")
	// Or use
	flag.StringVar(&c.AuthToken, "github-token", "", "GitHub token, authorize using env $GITHUB_TOKEN (convention)")
}

type Config struct {
	CommitMsg          string
	InputPath          string
	OutputRepoURL      string
	OutputRepoPathList string
	OutputBase         string
	OutputHead         string
	BasePR             string
	BaseMerge          string
	PrBody             string
	PrTitle            string
	// Allow a configured commit time to allow aligning GitOps commits to the original repo commit
	CommitTime TimeValue

	DryRun bool
	Depth  int

	WaitForTags GlobValue

	AuthUsername string
	AuthPassword string
	AuthOtp      string
	AuthToken    string
}

func (c *Config) ParseAndValidate() {
	flag.Parse()
	if c.OutputRepoURL == "" {
		log.Fatal("No output repository set")
	}
	if c.OutputHead == "" {
		c.OutputHead = fmt.Sprintf("auto/sync/%s", time.Now().Format("20060102T150405Z"))
	}
	if c.CommitMsg == "" {
		project := os.Getenv("CI_PROJECT_NAME")
		if project == "" {
			project, _ = os.Getwd()
		}
		refName := os.Getenv("CI_COMMIT_REF_NAME")
		if refName == "" {
			refName = "unknown"
		}
		c.CommitMsg = fmt.Sprintf("Sync %s/%s", project, refName)
	}
}

func (c *Config) OutputRepoPath() []string {
	return strings.Split(c.OutputRepoPathList, ",")
}
