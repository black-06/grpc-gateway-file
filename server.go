package gatewayfile

import (
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"
)

const (
	defaultBufSize = 1 << 20  // 1 MB
	maxMemory      = 32 << 20 // 32 MB. parameter for ReadForm.
)

func newUploadServerReader(server uploadServer, sizeLimit int64) *uploadServerReader {
	return &uploadServerReader{
		server:    server,
		sizeLimit: sizeLimit,
	}
}

func newDownloadServerWriter(server downloadServer, contentType string) *downloadServerWriter {
	return &downloadServerWriter{server: server, contentType: contentType, size: defaultBufSize}
}

// uploadServer is a client-stream server, see grpc.ClientStreamingServer
type uploadServer interface {
	grpc.ServerStream
	Recv() (*httpbody.HttpBody, error)
}

type uploadServerReader struct {
	server uploadServer
	buf    []byte

	sizeCurrent int64 // current size of the data in bytes
	sizeLimit   int64 // maximum size of the data in bytes (0 - unlimited)
}

func (reader *uploadServerReader) Read(dst []byte) (int, error) {
	src := reader.buf
	if len(reader.buf) == 0 {
		body, err := reader.server.Recv()
		if err != nil {
			return 0, err
		}
		src = body.Data
	}
	rn := len(src)
	if len(src) > len(dst) {
		rn = len(dst)
	}

	if reader.sizeLimit > 0 {
		if reader.sizeCurrent+int64(rn) > reader.sizeLimit {
			return 0, ErrSizeLimitExceeded
		}
		reader.sizeCurrent += int64(rn)
	}

	reader.buf = src[rn:]
	return copy(dst, src), nil
}

// downloadServer is a server-stream server, see grpc.ServerStreamingServer
type downloadServer interface {
	grpc.ServerStream
	Send(*httpbody.HttpBody) error
}

type downloadServerWriter struct {
	contentType string
	server      downloadServer
	size        int
}

func (writer *downloadServerWriter) Write(data []byte) (int, error) {
	n := 0
	for len(data) > 0 {
		wn := len(data)
		if wn >= writer.size {
			wn = writer.size
		}
		err := writer.server.Send(&httpbody.HttpBody{
			ContentType: writer.contentType,
			Data:        data[:wn],
		})
		if err != nil {
			return n, err
		}
		data = data[wn:]
		n += wn
	}
	return n, nil
}
