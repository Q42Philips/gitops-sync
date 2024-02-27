package main

import (
	"context"
	"log"
	"os"

	"github.com/Q42Philips/gitops-sync/pkg/config"
	"github.com/Q42Philips/gitops-sync/pkg/gitlogic"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/gobwas/glob"
)

func main() {
	Global := config.Config{}
	Global.Init()
	Global.ParseAndValidate()

	if len(os.Args) <= 3 {
		log.Fatal("Usage: wait [./repo] [commit hash] [gke_myproject_*]")
	}
	repo, err := git.PlainOpen(os.Args[1])
	if err != nil {
		log.Fatal("Error during git open", err)
	}

	commit := plumbing.NewHash(os.Args[2])
	Global.WaitForTags = config.GlobValue{Glob: glob.MustCompile(os.Args[3])}

	// Execute wait
	err = gitlogic.WaitForTags(context.Background(), Global, commit, repo)
	if err != nil {
		log.Fatal(err)
	} else {
		os.Exit(0)
	}
}
