package config

import (
	"log"
	"os"
	"time"

	"fmt"
	"log"
	"net/http"

	"github.com/jnovack/flag"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v33/github"
)

var Global Config

func init() {
	// flags
	flag.StringVar(&Global.CommitMsg, "message", "", "commit message, defaults to 'Sync ${CI_PROJECT_NAME:-$PWD}/$CI_COMMIT_REF_NAME to $OUTPUT_REPO_BRANCH")
	flag.StringVar(&Global.InputPath, "input-path", ".", "where to read artifacts from")
	flag.StringVar(&Global.OutputRepoURL, "output-repo", "", "where to write artifacts to")
	flag.StringVar(&Global.OutputRepoPath, "output-repo-path", ".", "where to write artifacts to")
	flag.StringVar(&Global.OutputBase, "output-base", "develop", "reference to use as basis")
	flag.StringVar(&Global.OutputHead, "output-head", "", "reference to write to & create a PR from into base; default = generated")
	flag.StringVar(&Global.BasePR, "pr", "", "whether to create a PR, and if set, which branch to set as PR base")
	flag.StringVar(&Global.BaseMerge, "merge", "", "whether to merge straight away, which branch to set as merge base")
	flag.StringVar(&Global.PrBody, "pr-body", "Sync", "Body of PR")
	flag.StringVar(&Global.PrTitle, "pr-title", "Sync", "Title of PR; defaults to commit message")
	flag.Var(&Global.CommitTime, "commit-timestamp", "Time of the commit; for example $CI_COMMIT_TIMESTAMP of the original commit (default: now)")

	flag.BoolVar(&Global.DryRun, "dry-run", false, "Do not push, merge, nor PR")
	flag.IntVar(&Global.Depth, "depth", 0, "Set the depth to do a shallow clone. Use with caution, go-git pushes can fail for shallow branches.")

	// Wait for tags
	flag.StringVar(&Global.WaitForTags, "wait-for-tags", "", "Wait for certain tags to update (glob patterns supported): example flux-sync or gke_myproject_*")

	// Authentication
	// Either use
	flag.StringVar(&Global.AuthUsername, "github-username", "", "GitHub username to use for basic auth")
	flag.StringVar(&Global.AuthPassword, "github-password", "", "GitHub password to use for basic auth")
	flag.StringVar(&Global.AuthOtp, "github-otp", "", "GitHub OTP to use for basic auth")
	// Or use
	flag.StringVar(&Global.AuthToken, "github-token", "", "GitHub token, authorize using env $GITHUB_TOKEN (convention)")
}

type Config struct {
	CommitMsg      string
	InputPath      string
	OutputRepoURL  string
	OutputRepoPath string
	OutputBase     string
	OutputHead     string
	BasePR         string
	BaseMerge      string
	PrBody         string
	PrTitle        string
	// Allow a configured commit time to allow aligning GitOps commits to the original repo commit
	CommitTime TimeValue

	DryRun bool
	Depth  int

	WaitForTags string

	AuthUsername string
	AuthPassword string
	AuthOtp      string
	AuthToken    string
}

func (c *Config) ParseAndValidate() {
	flag.Parse()
	if Global.OutputRepoURL == "" {
		log.Fatal("No output repository set")
	}
	if Global.OutputHead == "" {
		Global.OutputHead = fmt.Sprintf("auto/sync/%s", time.Now().Format("20060102T150405Z"))
	}
	if Global.CommitMsg == "" {
		project := os.Getenv("CI_PROJECT_NAME")
		if project == "" {
			project, _ = os.Getwd()
		}
		refName := os.Getenv("CI_COMMIT_REF_NAME")
		if refName == "" {
			refName = "unknown"
		}
		Global.CommitMsg = fmt.Sprintf("Sync %s/%s", project, refName)
	}
}

func (c *Config) GetClientAuth() (hubClient *github.Client, gitAuth githttp.AuthMethod) {
	if Global.AuthUsername != "" {
		hubAuth := &github.BasicAuthTransport{Username: c.AuthUsername, Password: c.AuthPassword, OTP: c.AuthOtp}
		hubClient = github.NewClient(hubAuth.Client())
		gitAuth = &BasicAuthWrapper{hubAuth}
	} else if c.AuthToken != "" {
		hubAuth := &github.BasicAuthTransport{Username: "x-access-token", Password: c.AuthToken}
		hubClient = github.NewClient(hubAuth.Client())
		gitAuth = &BasicAuthWrapper{hubAuth}
	} else {
		log.Fatal("No authentication provided. See help for authentication options.")
	}
	log.Println(gitAuth.String())
	return hubClient, gitAuth
}

var _ githttp.AuthMethod = &BasicAuthWrapper{}

type BasicAuthWrapper struct {
	*github.BasicAuthTransport
}

func (b *BasicAuthWrapper) Name() string {
	return "http-basic-auth"
}
func (b *BasicAuthWrapper) String() string {
	masked := "*******"
	if b.Password == "" {
		masked = "<empty>"
	}
	return fmt.Sprintf("%s - %s:%s", b.Name(), b.Username, masked)
}
func (b *BasicAuthWrapper) SetAuth(r *http.Request) {
	if b == nil {
		return
	}
	r.SetBasicAuth(b.Username, b.Password)
	if b.OTP != "" {
		r.Header.Set("X-GitHub-OTP", b.OTP)
	}
}
