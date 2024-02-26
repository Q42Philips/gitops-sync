package sync

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"slices"
	"strings"
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
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestMain(t *testing.T) {
	log.SetFlags(0)

	t.Run("Test that a single microservice is synced", func(t *testing.T) {
		state :=  NewTestSetup()
		performTest(t, state)
	})

	t.Run("Test that multiple output paths are synced", func(t *testing.T) {
		state := NewTestSetup()
		state.Global.OutputRepoPathList = "bases/microservice-a,apps/microservice-a"

		performTest(t, state)
	})
}

func performTest(t *testing.T, state State) {
	_, externalURL := prepareExternal()

	grp := errgroup.Group{}
	var (
		err error
		result1 Result
		result2 Result
		result3 Result
	)
	grp.Go(func() error {
		s := state.withFreshInput().withFreshOutput(externalURL)
		result1, err = s.syncBranch()
		return err
	})
	grp.Go(func() error {
		s := state.withFreshInput().withFreshOutput(externalURL)
		result2, err = s.syncBranch()
		return err
	})
	grp.Go(func() error {
		s := state.withFreshInput().withFreshOutput(externalURL)
		result3, err = s.syncBranch()
		return err
	})
	err = grp.Wait()
	orPanic(errors.WithStack(err), "sync")

	folders := strings.Split(state.Global.OutputRepoPathList, ",")
	checkCommit(t, result1.Commit, folders)
	checkCommit(t, result2.Commit, folders)
	checkCommit(t, result3.Commit, folders)
}

func checkCommit(t *testing.T, commit *object.Commit, modifiedFolders []string) {
	require.Equal(t, commit.Message, "sync")
	
	files, err := commit.Stats()
	orPanic(err, "files")

	for _, f := range files {
		if ! slices.ContainsFunc(modifiedFolders, func(s string) bool { return strings.Contains(f.Name, s) }) {
			assert.Fail(t, "unexpected modified file", f.Name)
		}
	}
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

func NewTestSetup() State {
	return State{
		orgName:  "Q42Philips",
		repoName: "gitops",
		Global: config.Config{
			OutputBase:         "production",
			OutputHead:         "feature/something",
			CommitMsg:          "sync",
			PrBody:             "body",
			PrTitle:            "title",
			OutputRepoPathList: "bases/microservice-a",
			CommitTime:         config.TimeValue(time.Date(2006, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC", 0))),
		},
	}
}
