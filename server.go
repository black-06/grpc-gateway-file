package gateway_file

import (
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"
)

// const defaultBufSize = 1 << 20 // 1 MB
const defaultBufSize = 10 * 1024 // 1 MB

func NewUploadServerReader(server UploadServer) *UploadServerReader {
	return &UploadServerReader{server: server}
}

func NewDownloadServerWriter(server DownloadServer, contentType string) *DownloadServerWriter {
	return &DownloadServerWriter{server: server, contentType: contentType, size: defaultBufSize}
}

// UploadServer is a client-stream server.
type UploadServer interface {
	grpc.ServerStream
	Recv() (*httpbody.HttpBody, error)
}

type UploadServerReader struct {
	server UploadServer
	buf    []byte
}

func (reader *UploadServerReader) Read(dst []byte) (int, error) {
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
	reader.buf = src[rn:]
	return copy(dst, src), nil
}

// DownloadServer is a server-stream server.
type DownloadServer interface {
	grpc.ServerStream
	Send(*httpbody.HttpBody) error
}

type DownloadServerWriter struct {
	contentType string
	server      DownloadServer
	size        int
}

func (writer *DownloadServerWriter) Write(data []byte) (int, error) {
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
