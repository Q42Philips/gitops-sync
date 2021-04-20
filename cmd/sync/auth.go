package sync

import (
	"fmt"
	"log"
	"net/http"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v33/github"
)

func getClientAuth() (hubClient *github.Client, gitAuth githttp.AuthMethod) {
	if *authUsername != "" {
		hubAuth := &github.BasicAuthTransport{Username: *authUsername, Password: *authPassword, OTP: *authOtp}
		hubClient = github.NewClient(hubAuth.Client())
		gitAuth = &BasicAuthWrapper{hubAuth}
	} else if *authToken != "" {
		hubAuth := &github.BasicAuthTransport{Username: "x-access-token", Password: *authToken}
		hubClient = github.NewClient(hubAuth.Client())
		gitAuth = &BasicAuthWrapper{hubAuth}
	} else {
		log.Fatal("No authentication provided. See help for authentication options.")
	}
	log.Println(gitAuth.String())
	return hubClient, gitAuth
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
