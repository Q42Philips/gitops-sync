package gitlogic

import (
	"bytes"
	"io"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/stretchr/testify/assert"
)

func TestRecursiveDelete(t *testing.T) {
	// Prepare
	fs := memfs.New()
	nested, err := ChrootMkdir(fs, "level1/level2/level3")
	assert.NoError(t, err)
	assert.NotNil(t, nested)
	f, err := nested.Create("dummy.txt")
	assert.NoError(t, err)
	_, err = io.Copy(f, bytes.NewBufferString("level3:foobar"))
	assert.NoError(t, err)
	f2, err := fs.Create("level1/dummy.txt")
	assert.NoError(t, err)
	_, err = io.Copy(f2, bytes.NewBufferString("level1:foobar"))
	assert.NoError(t, err)

	// Test
	err = RmRecursively(fs, "level1")
	assert.NoError(t, err)
	files, err := fs.ReadDir("level1")
	assert.NoError(t, err)
	assert.Len(t, files, 0)
}
