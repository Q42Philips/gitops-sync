package gitlogic

import (
	"log"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/koron-go/prefixw"
	"github.com/pkg/errors"
)

func Sync(gr *git.Repository, outputPath string, inputFs billy.Filesystem, commitOpt *git.CommitOptions, msg string) *object.Commit {
	// Do sync
	w, err := gr.Worktree()
	orFatal(err, "getting worktree")

	outputFs := w.Filesystem
	err = RmRecursively(outputFs, outputPath) // remove existing files
	orFatal(err, "removing old artifacts from fs")
	if outputPath != "." && outputPath != "" {
		outputFs, err = ChrootMkdir(outputFs, outputPath)
		orFatal(err, "failed to go to subdirectory")
	}
	err = Copy(inputFs, outputFs)
	orFatal(err, "copy files")
	err = addAllFiles(w)
	orFatal(err, "git add -A")

	// Print status
	status, err := w.Status()
	if len(status) == 0 {
		log.Println("No changes. Skipping commit.")
		head, err := gr.Head()
		orFatal(err, "getting head")
		obj, err := gr.CommitObject(head.Hash())
		orFatal(err, "getting commit")
		return obj
	}

	log.Println("Sync changes:")
	orFatal(err, "status")
	prefixw.New(log.Writer(), "> ").Write([]byte(status.String()))

	// Commit
	w.Status()
	hash, err := w.Commit(msg, commitOpt)
	orFatal(err, "committing")
	log.Println("Created commit", hash.String())
	obj, err := gr.CommitObject(hash)
	orFatal(err, "getting commit")
	return obj
}

func orFatal(err error, ctx string) {
	if err != nil {
		log.Fatal(errors.Wrap(err, ctx))
	}
}

// addAllFiles adds all files. Somehow w.AddWithOptions traverses only over
// the filesystem, therefore not removing any non-existing files on the
// filesystem from the index.
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
