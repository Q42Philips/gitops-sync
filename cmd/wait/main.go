package sync

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/Q42Philips/gitops-sync/pkg/githubutil"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/gobwas/glob"
	"github.com/google/go-github/v33/github"
	"github.com/jnovack/flag"
	"github.com/koron-go/prefixw"
	"github.com/pkg/errors"
)

// flags
var (
	outputRepoURL = flag.String("output-repo", "", "where to write artifacts to")
	depth         = flag.Int("depth", 0, "Set the depth to do a shallow clone. Use with caution, go-git pushes can fail for shallow branches.")

	// Wait for tags
	waitForTags = flag.String("wait-for-tags", "", "Wait for certain tags to update (glob patterns supported): example flux-sync or gke_myproject_*")
)

func init() {
	flag.Parse()

	if *outputRepoURL == "" {
		log.Fatal("No output repository set")
	}
}

func main() {
	orgName, repoName, err := githubutil.ParseGitHubRepo(*outputRepoURL)
	orFatal(err, "parsing url")

	var tagsGlob glob.Glob
	if waitForTags != nil && *waitForTags != "" {
		tagsGlob = glob.MustCompile(*waitForTags)
	}

	// Prepare output repository
	outputStorer := memory.NewStorage()
	outputFs := memfs.New()
	log.Printf("Cloning %s", maskURL(*outputRepoURL))
	outputRepo, err = git.Clone(outputStorer, outputFs, &git.CloneOptions{
		Auth:     gitAuth,
		Progress: prefixw.New(os.Stderr, "> "),
		URL:      *outputRepoURL,
		Depth:    *depth,
	})
	orFatal(err, "cloning")
	log.Println()

	log.Println("Fetching all refs")
	err = outputRepo.Fetch(&git.FetchOptions{
		Auth:     gitAuth,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{"refs/*:refs/*"},
		Depth:    *depth,
	})
	orFatal(err, "fetching (refs/*:refs/*)")
	log.Println()

	// Wait for all matching tags their history to include commit created above
	if tagsGlob != nil {
		watchedTags := make(map[string]*github.RepositoryTag)
		forEachTag(ctx, client, orgName, repoName, func(rt *github.RepositoryTag) {
			if tagsGlob.Match(rt.GetName()) {
				log.Println("Waiting for tag %s to include commit %q", rt.GetName(), obj.Hash.String())
				watchedTags[rt.GetName()] = rt
			}
		})
		if len(watchedTags) == 0 {
			log.Println("Found no matching tags to wait for. Done.")
			return
		}
		for {
			time.Sleep(1 * time.Second)
			log.Println("Fetching tags from origin")
			err = outputRepo.Fetch(&git.FetchOptions{
				Auth:     gitAuth,
				Progress: os.Stdout,
				RefSpecs: []config.RefSpec{"refs/tags/*:refs/tags/*"},
				Depth:    *depth,
				Force:    true,
			})
			for tagName := range watchedTags {

			}
		}
	}
}

func orFatal(err error, ctx string) {
	if err != nil {
		log.Fatal(errors.Wrap(err, ctx))
	}
}

func forEachTag(ctx context.Context, client *github.Client, orgName, repoName string, fn func(*github.RepositoryTag)) (err error) {
	var page = 0
	for {
		tags, resp, err := client.Repositories.ListTags(ctx, orgName, repoName, &github.ListOptions{Page: page})
		if err != nil {
			return err
		}
		for _, t := range tags {
			fn(t)
		}
		if resp.NextPage > 0 {
			page = resp.NextPage
		} else {
			break
		}
	}
	return nil
}
