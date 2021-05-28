package githubutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	org, name, err := ParseGitHubRepo("https://github.com/myorg/myrepo.git")
	assert.Equal(t, "myorg", org)
	assert.Equal(t, "myrepo", name)
	assert.NoError(t, err)
}
