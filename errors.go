package gatewayfile

import "errors"

var (
	ErrInvalidRange      = errors.New("invalid range")       // ErrInvalidRange - invalid range
	ErrSizeLimitExceeded = errors.New("size limit exceeded") // ErrSizeLimitExceeded - message too large
)
