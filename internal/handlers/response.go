package handlers

import (
	"errors"

	"seanime/internal/torrents/autoselect"
)

// SeaResponse is a generic response type for the API.
// It is used to return data or errors.
type SeaResponse[R any] struct {
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
	Data      R      `json:"data,omitempty"`
}

func NewDataResponse[R any](data R) SeaResponse[R] {
	res := SeaResponse[R]{
		Data: data,
	}
	return res
}

func NewErrorResponse(err error) SeaResponse[any] {
	if err == nil {
		return SeaResponse[any]{
			Error: "Unknown error",
		}
	}
	res := SeaResponse[any]{
		Error:     err.Error(),
		ErrorCode: errorCodeFor(err),
	}
	return res
}

func errorCodeFor(err error) string {
	switch {
	case errors.Is(err, autoselect.ErrNoTorrentsFound):
		return "NO_SOURCES"
	case errors.Is(err, autoselect.ErrNoFileFound):
		return "NO_MATCHING_FILE"
	default:
		return ""
	}
}
