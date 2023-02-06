package sync

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"testing"
	"time"

	"github.com/Q42Philips/gitops-sync/pkg/config"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	gconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
)

func TestMain(t *testing.T) {
	log.SetFlags(0)
	state := State{}
	state.fromTestSetup()

	_, externalURL := prepareExternal()

	grp := errgroup.Group{}
	var result Result
	var err error
	grp.Go(func() error {
		s := state.withFreshInput().withFreshOutput(externalURL)
		result, err = s.syncBranch()
		return err
	})
	grp.Go(func() error {
		s := state.withFreshInput().withFreshOutput(externalURL)
		result, err = s.syncBranch()
		return err
	})
	grp.Go(func() error {
		s := state.withFreshInput().withFreshOutput(externalURL)
		result, err = s.syncBranch()
		return err
	})
	err = grp.Wait()
	orPanic(errors.WithStack(err), "sync")
	assert.Equal(t, result.Commit.Message, "sync")
}

func (state State) withFreshInput() State {
	// Prepare begin state
	state.Global.InputPath, _ = os.MkdirTemp(os.TempDir(), "input")
	state.inputFs = osfs.New(state.Global.InputPath)
	orPanic(os.WriteFile(path.Join(state.Global.InputPath, "template.yaml"), []byte(`template: 1`), 0777), "write dummy file")
	return state
}

func prepareExternal() (*git.Repository, string) {
	output, _ := os.MkdirTemp(os.TempDir(), "output")
	repo, err := git.PlainInit(output, false)
	orPanic(err, "plain open init")
	w, _ := repo.Worktree()

	orPanic(errors.WithStack(err), "worktree")
	orPanic(os.WriteFile(path.Join(output, "README.md"), []byte("README"), 0777), "readme")
	w.Add("README.md")
	commit, err := w.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "F", Email: "f"}, Committer: &object.Signature{Name: "F", Email: "f"}})
	orPanic(err, "commit")

	orPanic(errors.WithStack(repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("production"), commit))), "branch")
	orPanic(errors.WithStack(repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("feature/something"), commit))), "branch")
	return repo, output
}

func (state State) withFreshOutput(url string) State {
	// Prepare output repository
	output, _ := os.MkdirTemp(os.TempDir(), fmt.Sprintf("output-%d", rand.Int()))
	var err error
	state.outputRepo, err = git.PlainClone(output, false, &git.CloneOptions{URL: url})
	orPanic(err, "plain clone ")
	err = state.outputRepo.Fetch(&git.FetchOptions{
		Progress: os.Stdout,
		RefSpecs: []gconfig.RefSpec{"refs/*:refs/*"},
	})
	orPanic(err, "fetch")
	state.worktree, err = state.outputRepo.Worktree()
	orPanic(err, "worktree")
	return state
}

func (state *State) fromTestSetup() {

	state.Global = config.Config{
		OutputBase:     "production",
		OutputHead:     "feature/something",
		CommitMsg:      "sync",
		PrBody:         "body",
		PrTitle:        "title",
		OutputRepoPath: "bases/microservice-a",
		CommitTime:     config.TimeValue(time.Date(2006, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC", 0))),
	}
	state.orgName = "Q42Philips"
	state.repoName = "gitops"

	return
}
