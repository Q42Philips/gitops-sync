package main

import (
	"context"
	"log"
	"os"

	"github.com/Q42Philips/gitops-sync/cmd/sync"
	. "github.com/Q42Philips/gitops-sync/pkg/config"
	"github.com/Q42Philips/gitops-sync/pkg/gitlogic"
)

// version information added by Goreleaser
var (
	version = "development"
	commit  = "development"
)

func init() {
	log.SetFlags(0)
	log.Printf("Running gitops-sync %s (%s)", version, commit)
}

func main() {
	result, err := sync.Main(Global)
	if err != nil {
		os.Exit(1)
	} else {
		os.Stdout.Write([]byte(result.Commit.String()))
	}

	if Global.WaitForTags.Glob != nil {
		err = gitlogic.WaitForTags(context.Background(), Global, result.Commit, result.Repository)
		if err != nil {
			os.Exit(1)
		} else {
			os.Exit(0)
		}
	}
}
