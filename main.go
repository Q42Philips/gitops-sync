package main

import (
	"context"
	"log"
	"os"

	"github.com/Q42Philips/gitops-sync/cmd/sync"
	"github.com/Q42Philips/gitops-sync/pkg/config"
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
	Global := config.Config{}
	Global.Init()
	Global.ParseAndValidate()

	result, err := sync.Main(Global)
	if err != nil {
		os.Exit(1)
	} else {
		os.Stdout.Write([]byte(result.Commit.String() + "\n"))
	}

	if Global.WaitForTags.Glob != nil {
		log.Printf("Waiting for tags (%q) to include synced commit", Global.WaitForTags.String())
		err = gitlogic.WaitForTags(context.Background(), Global, result.Commit.Hash, result.Repository)
		if err != nil {
			log.Printf("Error waiting for tags: %s", err)
			os.Exit(1)
		} else {
			os.Exit(0)
		}
	}
}
