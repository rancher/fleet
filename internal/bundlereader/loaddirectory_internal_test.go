package bundlereader

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaybeAddHelmRepoURLRegexHint(t *testing.T) {
	t.Run("returns nil for nil error", func(t *testing.T) {
		got := maybeAddHelmRepoURLRegexHint(nil, directory{})
		require.NoError(t, got)
	})

	t.Run("adds hint when credentials were stripped for empty regex", func(t *testing.T) {
		err := errors.New("unauthorized")
		got := maybeAddHelmRepoURLRegexHint(err, directory{strippedCreds: true})
		require.Error(t, got)
		require.ErrorIs(t, got, err)
		assert.Contains(t, got.Error(), helmRepoURLRegexUIHint)
	})

	t.Run("keeps original error when marker is not set", func(t *testing.T) {
		err := errors.New("unauthorized")
		got := maybeAddHelmRepoURLRegexHint(err, directory{strippedCreds: false})
		assert.Equal(t, err, got)
	})

	t.Run("keeps context cancellation errors unchanged", func(t *testing.T) {
		got := maybeAddHelmRepoURLRegexHint(context.Canceled, directory{strippedCreds: true})
		assert.Equal(t, context.Canceled, got)
	})

	t.Run("keeps context deadline errors unchanged", func(t *testing.T) {
		got := maybeAddHelmRepoURLRegexHint(context.DeadlineExceeded, directory{strippedCreds: true})
		assert.Equal(t, context.DeadlineExceeded, got)
	})
}
