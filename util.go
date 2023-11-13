package gateway_file

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
)

func Pick[T any](m map[string][]T, key string) (t T) {
	if len(m) == 0 {
		return t
	}
	values := m[key]
	if len(values) == 0 {
		return t
	}
	return values[0]
}

// HTTPRange copy from http.httpRange
type HTTPRange struct {
	Start, Length int64
}

func (r HTTPRange) ContentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.Start, r.Start+r.Length-1, size)
}

func (r HTTPRange) MIMEHeader(contentType string, size int64) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Range": {r.ContentRange(size)},
		"Content-Type":  {contentType},
	}
}

// ParseRange parses a Range header string as per RFC 7233.
func ParseRange(s string, size int64) ([]HTTPRange, error) {
	if s == "" {
		return nil, nil // header not present
	}
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, errors.New("invalid range")
	}
	var ranges []HTTPRange
	var noOverlap bool
	for _, ra := range strings.Split(s[len(b):], ",") {
		ra = textproto.TrimString(ra)
		if ra == "" {
			continue
		}
		start, end, ok := strings.Cut(ra, "-")
		if !ok {
			return nil, errors.New("invalid range")
		}
		start, end = textproto.TrimString(start), textproto.TrimString(end)
		var r HTTPRange
		if start == "" {
			// If no start is specified, end specifies the
			// range start relative to the end of the file,
			// and we are dealing with <suffix-length>
			// which has to be a non-negative integer as per
			// RFC 7233 Section 2.1 "Byte-Ranges".
			if end == "" || end[0] == '-' {
				return nil, errors.New("invalid range")
			}
			i, err := strconv.ParseInt(end, 10, 64)
			if i < 0 || err != nil {
				return nil, errors.New("invalid range")
			}
			if i > size {
				i = size
			}
			r.Start = size - i
			r.Length = size - r.Start
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i < 0 {
				return nil, errors.New("invalid range")
			}
			if i >= size {
				// If the range begins after the size of the content,
				// then it does not overlap.
				noOverlap = true
				continue
			}
			r.Start = i
			if end == "" {
				// If no end is specified, range extends to end of the file.
				r.Length = size - r.Start
			} else {
				i, err = strconv.ParseInt(end, 10, 64)
				if err != nil || r.Start > i {
					return nil, errors.New("invalid range")
				}
				if i >= size {
					i = size - 1
				}
				r.Length = i - r.Start + 1
			}
		}
		ranges = append(ranges, r)
	}
	if noOverlap && len(ranges) == 0 {
		// The specified ranges did not overlap with the content.
		return nil, errors.New("invalid range: failed to overlap")
	}
	return ranges, nil
}

// CountingWriter counts how many bytes have been written to it.
type CountingWriter int64

func (w *CountingWriter) Write(p []byte) (n int, err error) {
	*w += CountingWriter(len(p))
	return len(p), nil
}

// RangesMIMESize returns the number of bytes it takes to encode the
// provided ranges as a multipart response.
func RangesMIMESize(ranges []HTTPRange, contentType string, contentSize int64) (encSize int64) {
	var w CountingWriter
	mw := multipart.NewWriter(&w)
	for _, ra := range ranges {
		_, _ = mw.CreatePart(ra.MIMEHeader(contentType, contentSize))
		encSize += ra.Length
	}
	_ = mw.Close()
	encSize += int64(w)
	return
}

func SumRangesSize(ranges []HTTPRange) (size int64) {
	for _, ra := range ranges {
		size += ra.Length
	}
	return
}
