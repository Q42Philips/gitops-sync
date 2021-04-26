package gitlogic

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestSync(t *testing.T) {
	// Prepare git repo
	fs := memfs.New()
	storer := memory.NewStorage()
	repo, err := git.Init(storer, fs)
	assert.NoError(t, err)
	w, err := repo.Worktree()
	assert.NoError(t, err)

	// Some files
	fs.MkdirAll("bases/app1", 1444)
	writeFile(fs, "bases/app1/template.yaml", "[]")
	fs.MkdirAll("bases/app2", 1444)
	writeFile(fs, "bases/app2/template.yaml", "[]")
	writeFile(fs, "bases/app2/deprecated.md", "some markdown")

	// Initial commit
	err = addAllFiles(w)
	assert.NoError(t, err)
	hash, err := w.Commit("init", &git.CommitOptions{})
	assert.NoError(t, err)
	err = storer.SetReference(plumbing.NewHashReference("master", hash))
	assert.NoError(t, err)

	// Input fs
	inputFs := memfs.New()
	writeFile(inputFs, "template.yaml", "updated: true")

	// Test
	commit := Sync(repo, "bases/app2", inputFs, &git.CommitOptions{}, "sync")
	assert.NotNil(t, commit)
	changes, err := diff(repo, hash.String(), commit.Hash.String())
	assert.NoError(t, err)
	assert.NotNil(t, changes)

	// Assert diff is correct
	if assert.Len(t, changes, 2) {
		sort.SliceIsSorted(changes, func(i, j int) bool {
			nameI := firstDefined(changes[i].From.Name, changes[i].To.Name)
			nameJ := firstDefined(changes[j].From.Name, changes[j].To.Name)
			return strings.Compare(nameI, nameJ) < 0
		})
		assert.Equal(t, changes[0].From.Name, "bases/app2/deprecated.md")
		assert.Nil(t, changes[0].To.Tree)
		assert.Equal(t, changes[1].From.Name, "bases/app2/template.yaml")
	}

	// Assert which files there are in the commit
	err = w.Checkout(&git.CheckoutOptions{Hash: commit.Hash, Keep: false, Force: true})
	assert.NoError(t, err)
	files, err := fs.ReadDir("bases/app2")
	assert.NoError(t, err)
	assert.Len(t, files, 1)
}

func writeFile(fs billy.Filesystem, file string, contents string) error {
	f, err := fs.Create(file)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, bytes.NewBufferString(contents))
	defer f.Close()
	return err
}

// diff determines the changes between two commits given their sha
func diff(repo *git.Repository, before, after string) (diffs object.Changes, err error) {
	var commitA *object.Commit
	var commitB *object.Commit
	if commitA, err = repo.CommitObject(plumbing.NewHash(before)); err != nil {
		return nil, errors.Wrapf(err, "Getting sha %q", before)
	}
	if commitB, err = repo.CommitObject(plumbing.NewHash(after)); err != nil {
		return nil, errors.Wrapf(err, "Getting sha %q", after)
	}
	var treeA *object.Tree
	var treeB *object.Tree
	if treeA, err = commitA.Tree(); err != nil {
		return nil, errors.Wrapf(err, "Getting tree of %q", before)
	}
	if treeB, err = commitB.Tree(); err != nil {
		return nil, errors.Wrapf(err, "Getting tree of %q", after)
	}
	diffs, err = object.DiffTreeWithOptions(context.Background(), treeA, treeB, &object.DiffTreeOptions{DetectRenames: true})
	if err != nil {
		return nil, err
	}
	return
}

func firstDefined(strs ...string) string {
	for _, s := range strs {
		if s != "" {
			return s
		}
	}
	return ""
}
