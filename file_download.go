package gatewayfile

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// request headers, We parse it from the request via WithFileIncomingHeaderMatcher and store it in metadata.
const (
	headerRange             = "Range"
	headerIfRange           = "If-Range"
	headerIfMatch           = "If-Match"
	headerIfNoneMatch       = "If-None-Match"
	headerIfUnmodifiedSince = "If-Unmodified-Since"
	headerIfModifiedSince   = "If-Modified-Since"
)

// response headers, We temporarily store them in metadata,
// and later we will write it to the response in mux option, see WithFileForwardResponseOption
const (
	headerCode                = "code"
	headerAcceptRanges        = "accept-ranges"
	headerContentType         = "content-type"
	headerContentRange        = "content-range"
	headerContentLength       = "content-length"
	headerContentEncoding     = "content-encoding"
	headerContentDisposition  = "content-disposition"
	headerLastModified        = "last-modified"
	headerETag                = "etag"
	headerCacheControl        = "cache-control"
	headerXContentTypeOptions = "x-content-type-options"
	headerTransferEncoding    = "transfer-encoding"
)

// WithFileIncomingHeaderMatcher returns a ServeMuxOption representing a headerMatcher for incoming request to gateway.
// This matcher will be called with each header in http.Request. If matcher returns true, that header will be passed
// to gRPC context. To transform the header before passing to gRPC context, matcher should return modified header.
func WithFileIncomingHeaderMatcher() runtime.ServeMuxOption {
	return runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
		key = textproto.CanonicalMIMEHeaderKey(key)
		switch key {
		case headerRange,
			headerIfRange,
			headerIfMatch,
			headerIfNoneMatch,
			headerIfUnmodifiedSince,
			headerIfModifiedSince:
			return runtime.MetadataPrefix + key, true
		default:
			return runtime.DefaultHeaderMatcher(key)
		}
	})
}

// WithFileForwardResponseOption - forwardResponseOption is an option that will be called on the relevant
// context.Context, http.ResponseWriter, and proto.Message before every forwarded response.
func WithFileForwardResponseOption() runtime.ServeMuxOption {
	headers := []string{
		headerAcceptRanges,
		headerContentType,
		headerContentRange,
		headerContentLength,
		headerContentEncoding,
		headerContentDisposition,
		headerLastModified,
		headerETag,
		headerCacheControl,
		headerXContentTypeOptions,
		headerTransferEncoding,
	}
	return runtime.WithForwardResponseOption(func(ctx context.Context, writer http.ResponseWriter, message proto.Message) error {
		if message != nil {
			return nil
		}

		md, ok := runtime.ServerMetadataFromContext(ctx)
		if !ok {
			return fmt.Errorf("metadata not found")
		}
		for _, header := range headers {
			if v := pick(md.HeaderMD, header); v != "" {
				writer.Header().Set(header, v)
			}
		}
		if codeStr := pick(md.HeaderMD, headerCode); codeStr != "" {
			code, err := strconv.Atoi(codeStr)
			if err != nil {
				return err
			}
			writer.WriteHeader(code)
		}
		return nil
	})
}

// ServeFile comes from http.ServeFile, and made some adaptations for DownloadServer
func ServeFile(server downloadServer, contentType, path string) error {
	path = filepath.Clean(path)
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("invalid path %s", path)
	}
	return ServeContent(server, file, contentType, info.Name(), info.ModTime(), info.Size())
}

// ServeContent comes from http.ServeContent, and made some adaptations for DownloadServer
func ServeContent( //nolint:gocognit
	server downloadServer, content io.ReadSeeker, contentType, name string, modTime time.Time, size int64,
) error {
	outgoing := make(metadata.MD)
	incoming, _ := metadata.FromIncomingContext(server.Context())

	setLastModified(outgoing, modTime)
	done, rangeReq := checkPreconditions(outgoing, incoming, modTime)
	if done {
		return serveDone(server, outgoing)
	}

	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(name))
		if contentType == "" {
			// read a chunk to decide between utf-8 text and binary
			var buf [512]byte
			n, _ := io.ReadFull(content, buf[:])
			contentType = http.DetectContentType(buf[:n])
			// rewind to output whole file
			if _, err := content.Seek(0, io.SeekStart); err != nil {
				return serveError(server, outgoing, "seeker can't seek", http.StatusInternalServerError)
			}
		}
		outgoing.Set(headerContentType, contentType)
	}

	// handle Content-Range header.
	ranges, err := parseRange(rangeReq, size)
	switch err {
	case nil:
	case ErrNoOverlap:
		if size == 0 {
			// Some clients add a Range header to all requests to
			// limit the size of the response. If the file is empty,
			// ignore the range header and respond with a 200 rather
			// than a 416.
			ranges = nil
			break
		}
		outgoing.Set(headerContentRange, fmt.Sprintf("bytes */%d", size))
		fallthrough
	default:
		return serveError(server, outgoing, err.Error(), http.StatusRequestedRangeNotSatisfiable)
	}

	if sumRangesSize(ranges) > size {
		// The total number of bytes in all the ranges
		// is larger than the size of the file by
		// itself, so this is probably an attack, or a
		// dumb client. Ignore the range request.
		ranges = nil
	}

	var (
		sendCode              = http.StatusOK
		sendContent io.Reader = content
		sendSize              = size
	)
	if name != "" {
		outgoing.Set(headerContentDisposition, fmt.Sprintf("attachment; filename=%s", name))
	}

	switch {
	case len(ranges) == 1:
		// RFC 7233, Section 4.1:
		// "If a single part is being transferred, the server
		// generating the 206 response MUST generate a
		// Content-Range header field, describing what range
		// of the selected representation is enclosed, and a
		// payload consisting of the range.
		// ...
		// A server MUST NOT generate a multipart response to
		// a request for a single range, since a client that
		// does not request multiple parts might not support
		// multipart responses."
		ra := ranges[0]
		if _, err = content.Seek(ra.start, io.SeekStart); err != nil {
			return err
		}
		sendSize = ra.length
		sendCode = http.StatusPartialContent
		outgoing.Set(headerContentRange, ra.contentRange(size))
	case len(ranges) > 1:
		sendSize = rangesMIMESize(ranges, contentType, size)
		sendCode = http.StatusPartialContent

		pReader, pWriter := io.Pipe()
		mWriter := multipart.NewWriter(newDownloadServerWriter(server, contentType))

		outgoing.Set(headerContentType, "multipart/byteranges; boundary="+mWriter.Boundary())
		sendContent = pReader
		defer func() { _ = pReader.Close() }() // cause writing goroutine to fail and exit if CopyN doesn't finish.
		go func() {
			for _, ra := range ranges {
				part, err := mWriter.CreatePart(ra.mimeHeader(contentType, size))
				if err != nil {
					_ = pWriter.CloseWithError(err)
					return
				}
				if _, err := content.Seek(ra.start, io.SeekStart); err != nil {
					_ = pWriter.CloseWithError(err)
					return
				}
				if _, err := io.CopyN(part, content, ra.length); err != nil {
					_ = pWriter.CloseWithError(err)
					return
				}
			}
			_ = mWriter.Close()
			_ = pWriter.Close()
		}()
	}

	outgoing.Set(headerAcceptRanges, "bytes")
	// We should be able to unconditionally set the Content-Length here.
	//
	// However, there is a pattern observed in the wild that this breaks:
	// The user wraps the ResponseWriter in one which gzips data written to it,
	// and sets "Content-Encoding: gzip".
	//
	// The user shouldn't be doing this; the serveContent path here depends
	// on serving seekable data with a known length. If you want to compress
	// on the fly, then you shouldn't be using ServeFile/ServeContent, or
	// you should compress the entire file up-front and provide a seekable
	// view of the compressed data.
	//
	// However, since we've observed this pattern in the wild, and since
	// setting Content-Length here breaks code that mostly-works today,
	// skip setting Content-Length if the user set Content-Encoding.
	//
	// If this is a range request, always set Content-Length.
	// If the user isn't changing the bytes sent in the ResponseWrite,
	// the Content-Length will be correct.
	// If the user is changing the bytes sent, then the range request wasn't
	// going to work properly anyway and we aren't worse off.
	//
	// A possible future improvement on this might be to look at the type
	// of the ResponseWriter, and always set Content-Length if it's one
	// that we recognize.
	if len(ranges) > 0 || pick(outgoing, headerContentEncoding) == "" {
		outgoing.Set(headerContentLength, strconv.FormatInt(sendSize, 10))
		outgoing.Set(headerTransferEncoding, "identity")
	}
	outgoing.Set(headerCode, strconv.Itoa(sendCode))

	if err = server.SendHeader(outgoing); err != nil {
		return err
	}
	_, err = io.CopyN(newDownloadServerWriter(server, contentType), sendContent, sendSize)
	return err
}

func serveDone(server downloadServer, outgoing metadata.MD) error {
	return server.SendHeader(outgoing)
}

func serveError(server downloadServer, outgoing metadata.MD, text string, code int) error {
	for _, k := range []string{
		headerCacheControl,
		headerContentEncoding,
		headerETag,
		headerLastModified,
		headerContentLength,
	} {
		if _, ok := outgoing[k]; !ok {
			continue
		}
		outgoing.Delete(k)
	}

	contentType := "text/plain; charset=utf-8"
	outgoing.Set(headerContentType, contentType)
	outgoing.Set(headerXContentTypeOptions, "nosniff")
	outgoing.Set(headerCode, strconv.Itoa(code))

	if err := server.SendHeader(outgoing); err != nil {
		return err
	}
	return server.Send(&httpbody.HttpBody{
		ContentType: contentType,
		Data:        []byte(text),
	})
}

// scanETag determines if a syntactically valid ETag is present at s. If so,
// the ETag and remaining text after consuming ETag is returned. Otherwise,
// it returns "", "".
func scanETag(s string) (etag string, remain string) {
	s = textproto.TrimString(s)
	start := 0
	if strings.HasPrefix(s, "W/") {
		start = 2
	}
	if len(s[start:]) < 2 || s[start] != '"' {
		return "", ""
	}
	// ETag is either W/"text" or "text".
	// See RFC 7232 2.3.
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		switch {
		// Character values allowed in ETags.
		case c == 0x21 || c >= 0x23 && c <= 0x7E || c >= 0x80:
		case c == '"':
			return s[:i+1], s[i+1:]
		default:
			return "", ""
		}
	}
	return "", ""
}

// eTagStrongMatch reports whether a and b match using strong ETag comparison.
// Assumes a and b are valid ETags.
func eTagStrongMatch(a, b string) bool {
	return a == b && a != "" && a[0] == '"'
}

// eTagWeakMatch reports whether a and b match using weak ETag comparison.
// Assumes a and b are valid ETags.
func eTagWeakMatch(a, b string) bool {
	return strings.TrimPrefix(a, "W/") == strings.TrimPrefix(b, "W/")
}

// condResult is the result of an HTTP request precondition check.
// See https://tools.ietf.org/html/rfc7232 section 3.
type condResult int

const (
	condNone condResult = iota
	condTrue
	condFalse
)

func checkIfMatch(outgoing, incoming metadata.MD) condResult {
	im := pick(incoming, headerIfMatch)
	if im == "" {
		return condNone
	}
	for {
		im = textproto.TrimString(im)
		if len(im) == 0 {
			break
		}
		if im[0] == ',' {
			im = im[1:]
			continue
		}
		if im[0] == '*' {
			return condTrue
		}
		etag, remain := scanETag(im)
		if etag == "" {
			break
		}
		if eTagStrongMatch(etag, pick(outgoing, headerETag)) {
			return condTrue
		}
		im = remain
	}

	return condFalse
}

func checkIfUnmodifiedSince(incoming metadata.MD, modtime time.Time) condResult {
	ius := pick(incoming, headerIfUnmodifiedSince)
	if ius == "" || isZeroTime(modtime) {
		return condNone
	}
	t, err := http.ParseTime(ius)
	if err != nil {
		return condNone
	}

	// The Last-Modified header truncates sub-second precision so
	// the modtime needs to be truncated too.
	modtime = modtime.Truncate(time.Second)
	if ret := modtime.Compare(t); ret <= 0 {
		return condTrue
	}
	return condFalse
}

func checkIfNoneMatch(outgoing, incoming metadata.MD) condResult {
	inm := pick(incoming, headerIfNoneMatch)
	if inm == "" {
		return condNone
	}
	buf := inm
	for {
		buf = textproto.TrimString(buf)
		if len(buf) == 0 {
			break
		}
		if buf[0] == ',' {
			buf = buf[1:]
			continue
		}
		if buf[0] == '*' {
			return condFalse
		}
		etag, remain := scanETag(buf)
		if etag == "" {
			break
		}
		if eTagWeakMatch(etag, pick(outgoing, headerETag)) {
			return condFalse
		}
		buf = remain
	}
	return condTrue
}

func checkIfModifiedSince(incoming metadata.MD, modtime time.Time) condResult {
	ims := pick(incoming, headerIfModifiedSince)
	if ims == "" || isZeroTime(modtime) {
		return condNone
	}
	t, err := http.ParseTime(ims)
	if err != nil {
		return condNone
	}
	// The Last-Modified header truncates sub-second precision so
	// the modtime needs to be truncated too.
	modtime = modtime.Truncate(time.Second)
	if ret := modtime.Compare(t); ret <= 0 {
		return condFalse
	}
	return condTrue
}

func checkIfRange(outgoing, incoming metadata.MD, modtime time.Time) condResult {
	ir := pick(incoming, headerIfRange)
	if ir == "" {
		return condNone
	}
	etag, _ := scanETag(ir)
	if etag != "" {
		if eTagStrongMatch(etag, pick(outgoing, headerETag)) {
			return condTrue
		} else {
			return condFalse
		}
	}
	// The If-Range value is typically the ETag value, but it may also be
	// the modtime date. See golang.org/issue/8367.
	if modtime.IsZero() {
		return condFalse
	}
	t, err := http.ParseTime(ir)
	if err != nil {
		return condFalse
	}
	if t.Unix() == modtime.Unix() {
		return condTrue
	}
	return condFalse
}

var unixEpochTime = time.Unix(0, 0)

// isZeroTime reports whether t is obviously unspecified (either zero or Unix()=0).
func isZeroTime(t time.Time) bool {
	return t.IsZero() || t.Equal(unixEpochTime)
}

func setLastModified(outgoing metadata.MD, modTime time.Time) {
	if !isZeroTime(modTime) {
		outgoing.Set(headerLastModified, modTime.UTC().Format(time.RFC1123))
	}
}

func writeNotModified(outgoing metadata.MD) {
	// RFC 7232 section 4.1:
	// a sender SHOULD NOT generate representation metadata other than the
	// above listed fields unless said metadata exists for the purpose of
	// guiding cache updates (e.g., Last-Modified might be useful if the
	// response does not have an ETag field).
	outgoing.Delete(headerContentType)
	outgoing.Delete(headerContentLength)
	outgoing.Delete(headerContentEncoding)
	if pick(outgoing, headerETag) != "" {
		outgoing.Delete(headerLastModified)
	}
	outgoing.Set(headerCode, strconv.Itoa(http.StatusNotModified))
}

// checkPreconditions evaluates request preconditions and reports whether a precondition
// resulted in sending StatusNotModified or StatusPreconditionFailed.
func checkPreconditions(outgoing, incoming metadata.MD, modTime time.Time) (done bool, rangeHeader string) {
	// This function carefully follows RFC 7232 section 6.
	ch := checkIfMatch(outgoing, incoming)
	if ch == condNone {
		ch = checkIfUnmodifiedSince(incoming, modTime)
	}
	if ch == condFalse {
		outgoing.Set(headerCode, strconv.Itoa(http.StatusPreconditionFailed))
		return true, ""
	}
	switch checkIfNoneMatch(outgoing, incoming) {
	case condFalse:
		// Currently we cannot get the request method
		writeNotModified(outgoing)
		return true, ""
	case condNone:
		if checkIfModifiedSince(incoming, modTime) == condFalse {
			writeNotModified(outgoing)
			return true, ""
		}
	}

	rangeHeader = pick(incoming, headerRange)
	if rangeHeader != "" && checkIfRange(outgoing, incoming, modTime) == condFalse {
		rangeHeader = ""
	}
	return false, rangeHeader
}

// httpRange copy from http.httpRange
type httpRange struct {
	start, length int64
}

func (r httpRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.start+r.length-1, size)
}

func (r httpRange) mimeHeader(contentType string, size int64) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Range": {r.contentRange(size)},
		"Content-Type":  {contentType},
	}
}

// parseRange parses a Range header string as per RFC 7233.
func parseRange(s string, size int64) ([]httpRange, error) { //nolint:gocognit
	if s == "" {
		return nil, nil // header not present
	}
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, ErrInvalidRange
	}
	var (
		splitted  = strings.Split(s[len(b):], ",")
		ranges    = make([]httpRange, 0, len(splitted))
		noOverlap bool
	)
	for _, ra := range splitted {
		ra = textproto.TrimString(ra)
		if ra == "" {
			continue
		}
		start, end, ok := strings.Cut(ra, "-")
		if !ok {
			return nil, ErrInvalidRange
		}
		start, end = textproto.TrimString(start), textproto.TrimString(end)
		var r httpRange
		if start == "" {
			// If no start is specified, end specifies the
			// range start relative to the end of the file,
			// and we are dealing with <suffix-length>
			// which has to be a non-negative integer as per
			// RFC 7233 Section 2.1 "Byte-Ranges".
			if end == "" || end[0] == '-' {
				return nil, ErrInvalidRange
			}
			i, err := strconv.ParseInt(end, 10, 64)
			if i < 0 || err != nil {
				return nil, ErrInvalidRange
			}
			if i > size {
				i = size
			}
			r.start = size - i
			r.length = size - r.start
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i < 0 {
				return nil, ErrInvalidRange
			}
			if i >= size {
				// If the range begins after the size of the content,
				// then it does not overlap.
				noOverlap = true
				continue
			}
			r.start = i
			if end == "" {
				// If no end is specified, range extends to end of the file.
				r.length = size - r.start
			} else {
				i, err = strconv.ParseInt(end, 10, 64)
				if err != nil || r.start > i {
					return nil, ErrInvalidRange
				}
				if i >= size {
					i = size - 1
				}
				r.length = i - r.start + 1
			}
		}
		ranges = append(ranges, r)
	}
	if noOverlap && len(ranges) == 0 {
		// The specified ranges did not overlap with the content.
		return nil, ErrNoOverlap
	}
	return ranges, nil
}

// countingWriter counts how many bytes have been written to it.
type countingWriter int64

func (w *countingWriter) Write(p []byte) (n int, err error) {
	*w += countingWriter(len(p))
	return len(p), nil
}

// rangesMIMESize returns the number of bytes it takes to encode the
// provided ranges as a multipart response.
func rangesMIMESize(ranges []httpRange, contentType string, contentSize int64) (encSize int64) {
	var w countingWriter
	mw := multipart.NewWriter(&w)
	for _, ra := range ranges {
		_, _ = mw.CreatePart(ra.mimeHeader(contentType, contentSize))
		encSize += ra.length
	}
	_ = mw.Close()
	encSize += int64(w)
	return
}

func sumRangesSize(ranges []httpRange) (size int64) {
	for _, ra := range ranges {
		size += ra.length
	}
	return
}
