package wait

import (
	"context"
	"log"
	"os"
	"time"

	. "github.com/Q42Philips/gitops-sync/pkg/config"
	"github.com/Q42Philips/gitops-sync/pkg/githubutil"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-github/v33/github"
	"github.com/pkg/errors"
)

func Main(Global Config, commit plumbing.Hash, repo *git.Repository) {
	client, gitAuth := Global.GetClientAuth()
	ctx := context.Background()

	orgName, repoName, err := githubutil.ParseGitHubRepo(Global.OutputRepoURL)
	orFatal(err, "parsing url")

	log.Println("Fetching tags refs")
	err = repo.Fetch(&git.FetchOptions{
		Auth:     gitAuth,
		Progress: os.Stdout,
		RefSpecs: []config.RefSpec{"refs/tags/*:refs/tags/*"},
		Depth:    Global.Depth,
		Force:    true,
	})
	orFatal(err, "fetching (refs/*:refs/*)")
	log.Println()

	// Wait for all matching tags their history to include commit created above
	watchedTags := make(map[string]*github.RepositoryTag)
	forEachTag(ctx, client, orgName, repoName, func(rt *github.RepositoryTag) {
		if Global.WaitForTags.Match(rt.GetName()) {
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
		err = repo.Fetch(&git.FetchOptions{
			Auth:     gitAuth,
			Progress: os.Stdout,
			RefSpecs: []config.RefSpec{"refs/tags/*:refs/tags/*"},
			Depth:    Global.Depth,
			Force:    true,
		})
		for tagName := range watchedTags {

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
