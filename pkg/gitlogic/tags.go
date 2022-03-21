package gitlogic

import (
	"context"
	"log"
	"time"

	. "github.com/Q42Philips/gitops-sync/pkg/config"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/pkg/errors"
)

func WaitForTags(ctx context.Context, c Config, commit plumbing.Hash, repo *git.Repository) (err error) {
	var gitAuth transport.AuthMethod
	_, gitAuth, err = c.GetClientAuth()
	if err != nil {
		gitAuth, err = ssh.DefaultAuthBuilder("")
		if err != nil {
			return err
		}
		log.Println(gitAuth.String())
	}

	// Wait for all matching tags their history to include commit created before
	watchedTags := make(map[string]*object.Tag)
	watchedRefspec := []config.RefSpec{}
	tagIter, err := repo.Tags()
	if err != nil {
		return errors.Wrap(err, "listing tag objects")
	}
	err = tagIter.ForEach(func(r *plumbing.Reference) (err error) {
		if object, err := repo.TagObject(r.Hash()); err == nil {
			if c.WaitForTags.Match(object.Name) {
				log.Printf("Selected tag %s", object.Name)
				watchedTags[object.Name] = object
				watchedRefspec = append(watchedRefspec, config.RefSpec(r.Name()+":"+r.Name()))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(watchedTags) == 0 {
		return errors.New("found no matching tags to wait for")
	}

	for {
		// (Re-)fetch all tags
		log.Println("Fetching tags refs")
		err = repo.Fetch(&git.FetchOptions{
			Auth:     gitAuth,
			RefSpecs: watchedRefspec,
			Depth:    c.Depth,
			Force:    true,
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			// If the tag is removed from the remote, we should remove it too
			var errNoMatching = git.NoMatchingRefSpecError{}
			if isRemoteMissing := errors.As(err, &errNoMatching); isRemoteMissing {
				return errors.Wrap(err, "failed to wait because one of the tags disappeared")
			}
			return errors.Wrap(err, "fetching tag refs")
		}

		var needsSync = make(map[string]bool)
		for name, t := range watchedTags {
			// get latest tagObject
			ref, err := repo.Tag(name)
			if err != nil {
				log.Printf("%s (last sync %s ago) failed to verify: %s", name, time.Since(t.Tagger.When), err)
				continue
			}
			t, err = repo.TagObject(ref.Hash())
			if err != nil {
				log.Printf("%s (last sync %s ago) failed to verify: %s", name, time.Since(t.Tagger.When), err)
				continue
			}
			watchedTags[name] = t

			// check if the tag points to the commit or if it is an ancestor of the commit (for when the tag is updated after our commit)
			match, e := hasAncestor(repo, t.Target, commit)
			if e != nil || !match {
				needsSync[name] = true
				if e != nil {
					log.Printf("%s (last sync %s ago) failed to verify: %s", name, time.Since(t.Tagger.When), e)
				} else {
					log.Printf("%s (last sync %s ago) is not yet in sync", name, time.Since(t.Tagger.When))
				}
			} else {
				log.Println(name, "is up-to-date")
			}
		}
		if len(needsSync) == 0 {
			log.Printf("All tags include commit %q", commit)
			break
		}

		// Loop after sleep
		time.Sleep(2 * time.Second)
	}
	return nil
}

func hasAncestor(repo *git.Repository, leaf plumbing.Hash, root plumbing.Hash) (bool, error) {
	// If we have reached the ancestor, return true
	if root == leaf {
		return true, nil
	}

	// Get the commits or fail
	rootCommit, err := repo.CommitObject(root)
	if err != nil {
		return false, errors.Wrapf(err, "commit %q", root)
	}
	leafCommit, err := repo.CommitObject(leaf)
	if err != nil {
		return false, errors.Wrapf(err, "commit %q", root)
	}

	return rootCommit.IsAncestor(leafCommit)
}
