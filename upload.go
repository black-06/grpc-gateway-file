package gateway_file

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/pkg/errors"
	"google.golang.org/grpc/metadata"
)

func SaveMultipartFile(header *multipart.FileHeader, path string) error {
	file, err := header.Open()
	if err != nil {
		return errors.Wrapf(err, "open file failed")
	}

	if f, ok := file.(*os.File); ok {
		// Windows can't rename files that are opened.
		if err = f.Close(); err != nil {
			return errors.Wrapf(err, "colse file failed")
		}

		// If renaming fails we try the normal copying method.
		// Renaming could fail if the files are on different devices.
		if err = os.Rename(f.Name(), path); err == nil {
			return nil
		}

		// Reopen f for the code below.
		if file, err = header.Open(); err != nil {
			return errors.Wrapf(err, "open file failed")
		}
	}

	defer func() { _ = file.Close() }()

	output, err := os.Create(path)
	if err != nil {
		return errors.Wrapf(err, "create output file failed")
	}
	defer func() { _ = output.Close() }()

	_, err = io.Copy(output, file)
	return errors.Wrapf(err, "copy file failed")
}

// MultipartFormHeader returns the first file for the provided form key.
// rpc request params must be 'stream google.api.HttpBody'
func MultipartFormHeader(server UploadServer, key string) (*multipart.FileHeader, error) {
	form, err := ParseMultipartForm(server)
	if err != nil {
		return nil, errors.Wrapf(err, "parse multipart form failed")
	}
	if headers := form.File[key]; len(headers) > 0 {
		return headers[0], nil
	}
	return nil, http.ErrMissingFile
}

func ParseMultipartForm(server UploadServer) (*multipart.Form, error) {
	md, _ := metadata.FromIncomingContext(server.Context())
	boundary, err := parseBoundary(md)
	if err != nil {
		return nil, err
	}

	reader := multipart.NewReader(NewUploadServerReader(server), boundary)
	return reader.ReadForm(100 << 20)
}

func parseBoundary(md metadata.MD) (string, error) {
	contentType := Pick(md, "grpcgateway-content-type")
	if contentType == "" {
		return "", http.ErrNotMultipart
	}
	d, params, err := mime.ParseMediaType(contentType)
	if err != nil || !(d == "multipart/form-data" || d == "multipart/mixed") {
		return "", http.ErrNotMultipart
	}
	boundary, ok := params["boundary"]
	if !ok {
		return "", http.ErrMissingBoundary
	}
	return boundary, nil
}
