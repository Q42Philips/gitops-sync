package sync

import (
	"io"
	"os"

	"github.com/go-git/go-billy/v5"
)

func chrootMkdir(fs billy.Filesystem, path string) (out billy.Filesystem, err error) {
	if err = fs.MkdirAll(path, 1444); err != nil {
		return nil, err
	}
	out, err = fs.Chroot(path)
	return
}

func rmRecursively(fs billy.Filesystem, path string) error {
	st, err := fs.Stat(path)
	// Already non-existing
	if err == os.ErrNotExist {
		return nil
	}
	// Not a dir
	if !st.IsDir() {
		return fs.Remove(path)
	}
	// Dir, list all files
	files, err := fs.ReadDir(path)
	if err != nil {
		return err
	}
	// Delete any files
	if len(files) > 0 {
		chroot, err := fs.Chroot(path)
		if err != nil {
			return err
		}
		for _, f := range files {
			rmRecursively(chroot, f.Name())
		}
	}
	// Finally delete empty dir
	return fs.Remove(path)
}

func copy(fs1 billy.Filesystem, fs2 billy.Filesystem) error {
	files, err := fs1.ReadDir(".")
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			var sub1 billy.Filesystem
			var sub2 billy.Filesystem
			if sub1, err = fs1.Chroot(f.Name()); err != nil {
				return err
			}
			if sub2, err = chrootMkdir(fs2, f.Name()); err != nil {
				return err
			}
			if err = copy(sub1, sub2); err != nil {
				return err
			}
		} else {
			var f1 billy.File
			var f2 billy.File
			if f2, err = fs2.Create(f.Name()); err != nil {
				return err
			}
			defer f2.Close()
			if f1, err = fs1.Open(f.Name()); err != nil {
				return err
			}
			defer f1.Close()
			if _, err = io.Copy(f2, f1); err != nil {
				return err
			}
		}
	}
	return nil
}
