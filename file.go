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
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const (
	headerCode             = "code"
	headerAcceptRanges     = "accept-ranges"
	headerTransferEncoding = "transfer-encoding"

	headerContentRange       = "content-range"
	headerContentLength      = "content-length"
	headerContentDisposition = "content-disposition"
	headerLastModified       = "last-modified"
)

// WithFileIncomingHeaderMatcher returns a ServeMuxOption representing a headerMatcher for incoming request to gateway.
// This matcher will be called with each header in http.Request. If matcher returns true, that header will be passed to gRPC context. To transform the header before passing to gRPC context, matcher should return modified header.
func WithFileIncomingHeaderMatcher() runtime.ServeMuxOption {
	return runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
		key = textproto.CanonicalMIMEHeaderKey(key)
		switch key {
		case "Range":
			return runtime.MetadataPrefix + key, true
		default:
			return runtime.DefaultHeaderMatcher(key)
		}
	})
}

// WithFileForwardResponseOption - forwardResponseOption is an option that will be called on the relevant context.Context, http.ResponseWriter, and proto.Message before every forwarded response.
func WithFileForwardResponseOption() runtime.ServeMuxOption {
	headers := []string{
		headerAcceptRanges,
		headerTransferEncoding,
		headerContentRange,
		headerContentLength,
		headerContentDisposition,
		headerLastModified,
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
			code, err := strconv.ParseInt(codeStr, 10, 64)
			if err != nil {
				return err
			}
			writer.WriteHeader(int(code))
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
	return serveContent(server, file, contentType, info.Name(), info.ModTime(), info.Size())
}

// serveContent comes from http.serveContent, and made some adaptations for DownloadServer
func serveContent( //nolint:gocognit
	server downloadServer, content io.ReadSeeker, contentType, name string, modTime time.Time, size int64,
) error {
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(name))
		if contentType == "" {
			// read a chunk to decide between utf-8 text and binary
			var buf [512]byte
			n, _ := io.ReadFull(content, buf[:])
			contentType = http.DetectContentType(buf[:n])
			// rewind to output whole file
			if _, err := content.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("seeker can't seek %w", err)
			}
		}
	}

	// handle Content-Range header.
	md, _ := metadata.FromIncomingContext(server.Context())
	ranges, err := parseRange(pick(md, "grpcgateway-range"), size)
	if err != nil {
		return err
	}
	if sumRangesSize(ranges) > size {
		// The total number of bytes in all the ranges
		// is larger than the size of the file by
		// itself, so this is probably an attack, or a
		// dumb client. Ignore the range request.
		ranges = nil
	}

	var (
		sendCode                         = http.StatusOK
		sendContent            io.Reader = content
		sendContentType                  = contentType
		sendContentRange       string
		sendContentLength      = size
		sendContentDisposition string
	)
	if name != "" {
		sendContentDisposition = fmt.Sprintf("attachment; filename=%s", name)
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
		if _, err = content.Seek(ra.Start, io.SeekStart); err != nil {
			return err
		}
		sendContentLength = ra.Length
		sendCode = http.StatusPartialContent
		sendContentRange = ra.ContentRange(size)
	case len(ranges) > 1:
		sendContentLength = rangesMIMESize(ranges, contentType, size)
		sendCode = http.StatusPartialContent

		reader, writer := io.Pipe()
		mWriter := multipart.NewWriter(newDownloadServerWriter(server, contentType))

		sendContentType = "multipart/byteranges; boundary=" + mWriter.Boundary()
		sendContent = reader
		defer func() { _ = reader.Close() }() // cause writing goroutine to fail and exit if CopyN doesn't finish.

		go func() {
			var err error //nolint:govet
			defer func() {
				if err != nil {
					_ = writer.CloseWithError(err)
				} else {
					_ = mWriter.Close()
					_ = writer.Close()
				}
			}()
			for _, ra := range ranges {
				var part io.Writer
				if part, err = mWriter.CreatePart(ra.MIMEHeader(contentType, size)); err != nil {
					return
				}
				if _, err = content.Seek(ra.Start, io.SeekStart); err != nil {
					return
				}
				if _, err = io.CopyN(part, content, ra.Length); err != nil {
					return
				}
			}
		}()
	}

	err = server.SendHeader(metadata.New(map[string]string{
		headerAcceptRanges:       "bytes",
		headerTransferEncoding:   "identity",
		headerCode:               strconv.FormatInt(int64(sendCode), 10),
		headerContentLength:      strconv.FormatInt(sendContentLength, 10),
		headerContentRange:       sendContentRange,
		headerContentDisposition: sendContentDisposition,
		headerLastModified:       modTime.UTC().Format(time.RFC1123),
	}))
	if err != nil {
		return err
	}
	_, err = io.CopyN(newDownloadServerWriter(server, sendContentType), sendContent, sendContentLength)
	return err
}
