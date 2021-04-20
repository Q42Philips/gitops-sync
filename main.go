package main

import (
	"log"

	"github.com/Q42Philips/gitops-sync/cmd/sync"
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
	sync.Main()
}
