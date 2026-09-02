package vendor

import (
	"context"
	"strings"

	getter "github.com/hashicorp/go-getter/v2"
)

// gitFetcher vendors git sources via go-getter, which already implements address detection/cloning for the forms Terraform's own module.source supports (git::..., github.com/org/repo, git@host:path, etc.).
type gitFetcher struct{}

// gitAddressHints are checked before attempting an actual fetch, purely so an unsupported source shape (e.g. a future Terraform Registry address, not yet implemented) fails with a clear "no fetcher recognizes this source" instead of a confusing error from deep inside go-getter.
var gitAddressHints = []string{"git::", "git@", "github.com/", "gitlab.com/", "bitbucket.org/"}

func (gitFetcher) Matches(src string) bool {
	if strings.HasSuffix(src, ".git") {
		return true
	}
	for _, hint := range gitAddressHints {
		if strings.HasPrefix(src, hint) {
			return true
		}
	}
	return false
}

func (gitFetcher) Fetch(ctx context.Context, src, dst string) error {
	_, err := getter.Get(ctx, dst, src)
	return err
}
