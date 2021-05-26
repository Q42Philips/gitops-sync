package main

import (
	"log"
	"os"

	"github.com/Q42Philips/gitops-sync/cmd/sync"
	. "github.com/Q42Philips/gitops-sync/pkg/config"
)

// version information added by Goreleaser
var (
	version = "development"
	commit  = "development"
)

func init() {
	log.SetFlags(0)
	log.Printf("Running gitops-sync %s (%s)", version, commit)
	Global.ParseAndValidate()
}

func main() {
	result, err := sync.Main(Global)
	if err != nil {
		os.Exit(1)
	} else {
		os.Stdout.Write([]byte(result.Commit.String()))
	}
}
