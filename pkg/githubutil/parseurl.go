package githubutil

import (
	"errors"
	"net/url"
	"strings"
)

func ParseGitHubRepo(u string) (org, repo string, err error) {
	p, err := url.Parse(u)
	if err != nil {
		return "", "", err
	}
	pathSegments := strings.Split(strings.Trim(strings.TrimRight(p.Path, ".git"), "/"), "/")
	if len(pathSegments) < 2 {
		return "", "", errors.New("invalid github url")
	}
	return pathSegments[0], pathSegments[1], nil
}
