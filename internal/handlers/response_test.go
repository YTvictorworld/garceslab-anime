package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"seanime/internal/torrents/autoselect"
)

func TestAutoSelectErrorsHaveStableAPIClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{
			name: "no sources",
			err:  fmt.Errorf("auto-select: %w with any resolution", autoselect.ErrNoTorrentsFound),
			code: "NO_SOURCES",
		},
		{
			name: "no matching file",
			err:  fmt.Errorf("failed to choose episode: %w", autoselect.ErrNoFileFound),
			code: "NO_MATCHING_FILE",
		},
		{
			name: "provider error stays generic",
			err:  errors.New("provider timed out"),
			code: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := NewErrorResponse(tt.err)
			require.Equal(t, tt.code, response.ErrorCode)
			if tt.code == "" {
				require.Equal(t, http.StatusInternalServerError, statusCodeForError(tt.err))
			} else {
				require.Equal(t, http.StatusUnprocessableEntity, statusCodeForError(tt.err))
			}
		})
	}
}
