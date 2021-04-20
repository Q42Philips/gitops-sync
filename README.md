# GitOps Sync cli
This tools job is to copy provided artifacts to a GitOps repository.

Settings/inputs:
- a file system
- whitelist which files to copy
- destination
  1. git repository url
  2. path
  3. branch name
  4. PR contents

You must ensure the git repository is writable by providing the right authorization, either via 1) the url, or 2) via a SSH private key, or 3) an OAuth token in `$GITHUB_TOKEN`.

Currently only works using GitHub. Please consider forking and adding GitLab support if needed.

Usage:
```
make build

dotenv -f sync.env bin/sync -output-repo https://github.com/yourorg/gitops.git -output-base=develop -output-head=test-sync
```

### References
1. See some `go-git` examples in https://github.com/go-git/go-git/tree/master/_examples/
