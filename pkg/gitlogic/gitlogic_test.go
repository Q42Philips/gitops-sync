package gitlogic

import (
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/stretchr/testify/assert"
)

func TestRecursiveDelete(t *testing.T) {
	// Prepare
	fs := memfs.New()
	nested, err := ChrootMkdir(fs, "level1/level2/level3")
	assert.NoError(t, err)
	err = writeFile(nested, "dummy.txt", "level3:foobar")
	assert.NoError(t, err)
	err = writeFile(fs, "level1/dummy.txt", "level1:foobar")
	assert.NoError(t, err)

	// Test
	err = RmRecursively(fs, "level1")
	assert.NoError(t, err)
	files, err := fs.ReadDir("level1")
	assert.NoError(t, err)
	assert.Len(t, files, 0)
}
