package debrid_client

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"seanime/internal/torrents/autoselect"
)

func TestPreserveAutoSelectError(t *testing.T) {
	t.Run("wrapped no-source error keeps its type", func(t *testing.T) {
		input := fmt.Errorf("no torrents found with any resolution: %w", autoselect.ErrNoTorrentsFound)

		got := preserveAutoSelectError(input)

		require.ErrorIs(t, got, autoselect.ErrNoTorrentsFound)
		require.ErrorContains(t, got, "please select manually")
	})

	t.Run("other errors are returned unchanged", func(t *testing.T) {
		input := errors.New("provider timed out")

		require.Same(t, input, preserveAutoSelectError(input))
	})
}
