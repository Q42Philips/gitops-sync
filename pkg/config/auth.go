package config

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v33/github"
)

func (c *Config) GetClientAuth() (hubClient *github.Client, gitAuth githttp.AuthMethod, err error) {
	if c.AuthUsername != "" {
		hubAuth := &github.BasicAuthTransport{Username: c.AuthUsername, Password: c.AuthPassword, OTP: c.AuthOtp}
		hubClient = github.NewClient(hubAuth.Client())
		gitAuth = &BasicAuthWrapper{hubAuth}
	} else if c.AuthToken != "" {
		hubAuth := &github.BasicAuthTransport{Username: "x-access-token", Password: c.AuthToken}
		hubClient = github.NewClient(hubAuth.Client())
		gitAuth = &BasicAuthWrapper{hubAuth}
	} else {
		return nil, nil, errors.New("no authentication provided, see help for authentication options")
	}
	log.Println(gitAuth.String())
	return hubClient, gitAuth, nil
}

var _ githttp.AuthMethod = &BasicAuthWrapper{}

type BasicAuthWrapper struct {
	*github.BasicAuthTransport
}

func (b *BasicAuthWrapper) Name() string {
	return "http-basic-auth"
}
func (b *BasicAuthWrapper) String() string {
	masked := "*******"
	if b.Password == "" {
		masked = "<empty>"
	}
	return fmt.Sprintf("%s - %s:%s", b.Name(), b.Username, masked)
}
func (b *BasicAuthWrapper) SetAuth(r *http.Request) {
	if b == nil {
		return
	}
	r.SetBasicAuth(b.Username, b.Password)
	if b.OTP != "" {
		r.Header.Set("X-GitHub-OTP", b.OTP)
	}
}
