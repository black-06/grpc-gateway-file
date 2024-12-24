package gatewayfile

import "errors"

var (
	ErrInvalidRange      = errors.New("invalid range")       // ErrInvalidRange - invalid range
	ErrSizeLimitExceeded = errors.New("size limit exceeded") // ErrSizeLimitExceeded - message too large
	// ErrNoOverlap is returned by serveContent's parseRange if first-byte-pos of
	// all of the byte-range-spec values is greater than the content size.
	ErrNoOverlap = errors.New("invalid range: failed to overlap")
)
