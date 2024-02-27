package gitlogic

import (
	"io"
	"os"

	"github.com/go-git/go-billy/v5"
)

// ChrootMkdir creates the directory and descends, returning a chrooted filesystem to that dir
func ChrootMkdir(fs billy.Filesystem, path string) (billy.Filesystem, error) {
	if err := fs.MkdirAll(path, 1444); err != nil {
		return nil, err
	}
	out, err := fs.Chroot(path)
	return out, err
}

// RmRecursively removes are directory recursively
func RmRecursively(fs billy.Filesystem, path string) error {
	st, err := fs.Stat(path)
	// Already non-existing
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
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
			err := RmRecursively(chroot, f.Name())
			if err != nil {
				return err
			}
		}
	}
	// Finally delete empty dir
	return fs.Remove(path)
}

// Copy writes all files from fs1 to fs2
func Copy(fs1 billy.Filesystem, fs2 billy.Filesystem) error {
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
			if sub2, err = ChrootMkdir(fs2, f.Name()); err != nil {
				return err
			}
			if err = Copy(sub1, sub2); err != nil {
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
