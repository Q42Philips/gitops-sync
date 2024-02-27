package gitlogic

import (
	"log"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/koron-go/prefixw"
	"github.com/pkg/errors"
)

// Sync syncs the input filesystem to the output paths in the git repository.
func Sync(gr *git.Repository, outputPaths []string, inputFs billy.Filesystem, commitOpt *git.CommitOptions, msg string) (*object.Commit, error) {
	// Do sync
	w, err := gr.Worktree()
	if err != nil {
		return nil, errors.Wrap(err, "getting worktree")
	}

	baseFs := w.Filesystem
	outputs := make([]billy.Filesystem, 0)

	log.Println("Cleaing up files")
	for _, p := range outputPaths {
		// remove existing files
		err = RmRecursively(baseFs, p)
		if err != nil {
			return nil, errors.Wrapf(err, "removing old artifacts from fs: %s", p)
		}

		if p != "." && p != "" {
			o, err := ChrootMkdir(baseFs, p)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to go or create to subdirectory: %s", p)
			}
			outputs = append(outputs, o)
		}
	}

	log.Println("Copying files")
	for _, op := range outputs {
		err = Copy(inputFs, op)
		if err != nil {
			return nil, errors.Wrap(err, "copying files")
		}
	}

	log.Println("Adding files to git")
	err = addAllFiles(w)
	if err != nil {
		return nil, errors.Wrap(err, "adding files to git commit")
	}

	// Print status
	status, err := w.Status()
	if len(status) == 0 {
		log.Println("No changes. Skipping commit.")
		head, err := gr.Head()
		if err != nil {
			return nil, errors.Wrap(err, "getting git head")
		}
		obj, err := gr.CommitObject(head.Hash())
		if err != nil {
			return nil, errors.Wrap(err, "getting commit")
		}
		return obj, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "status")
	}

	log.Println("Sync changes:")
	_, err = prefixw.New(log.Writer(), "> ").Write([]byte(status.String()))
	if err != nil {
		return nil, errors.Wrap(err, "writing status")
	}

	// Commit
	_, err = w.Status()
	if err != nil {
		return nil, errors.Wrap(err, "status")
	}
	hash, err := w.Commit(msg, commitOpt)
	if err != nil {
		return nil, errors.Wrap(err, "committing")
	}

	log.Println("Created commit", hash.String())
	obj, err := gr.CommitObject(hash)
	if err != nil {
		return nil, errors.Wrap(err, "getting commit")
	}

	return obj, nil
}

// addAllFiles is "git add -A".
// Somehow w.AddWithOptions traverses only over the filesystem, therefore
// not removing any non-existing files on the filesystem from the index.
func addAllFiles(w *git.Worktree) error {
	files, err := w.Status()
	if err != nil {
		return err
	}
	for filename, s := range files {
		if s.Staging == git.Unmodified && s.Worktree == git.Unmodified {
			continue
		}
		if _, err = w.Add(filename); err != nil {
			return err
		}
	}
	return nil
}
