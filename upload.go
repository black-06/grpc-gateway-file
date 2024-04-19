package gatewayfile

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"google.golang.org/grpc/metadata"
)

// SaveMultipartFile saves the provided multipart file to the given path.
func SaveMultipartFile(header *multipart.FileHeader, path string) error {
	file, err := header.Open()
	if err != nil {
		return fmt.Errorf("open file failed %w", err)
	}

	if f, ok := file.(*os.File); ok {
		// Windows can't rename files that are opened.
		if err = f.Close(); err != nil {
			return fmt.Errorf("close file failed %w", err)
		}

		// If renaming fails we try the normal copying method.
		// Renaming could fail if the files are on different devices.
		if err = os.Rename(f.Name(), path); err == nil {
			return nil
		}

		// Reopen f for the code below.
		if file, err = header.Open(); err != nil {
			return fmt.Errorf("open file failed %w", err)
		}
	}

	defer func() { _ = file.Close() }()

	// Sanitize the path variable to prevent potential file inclusion.
	path = filepath.Clean(path)

	output, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create output file failed %w", err)
	}
	defer func() { _ = output.Close() }()

	_, err = io.Copy(output, file)
	if err != nil {
		return fmt.Errorf("copy file failed %w", err)
	}

	return nil
}

// FormData is a wrapper around multipart.Form.
type FormData struct {
	form *multipart.Form
}

// NewFormData returns a new FormData.
// sizeLimit is the maximum size of the form data in bytes (0 = unlimited).
func NewFormData(server uploadServer, sizeLimit int64) (*FormData, error) {
	form, err := parseMultipartForm(server, sizeLimit)
	if err != nil {
		return nil, fmt.Errorf("parse multipart form failed %w", err)
	}
	return &FormData{form: form}, nil
}

// Files returns the files for the provided form key
func (f *FormData) Files(key string) []*multipart.FileHeader {
	if headers := f.form.File[key]; len(headers) > 0 {
		return headers
	}
	return nil
}

// FirstFile returns the first file for the provided form key
func (f *FormData) FirstFile(key string) *multipart.FileHeader {
	headers := f.Files(key)
	if len(headers) == 0 {
		return nil
	}

	return headers[0]
}

// Values returns the values for the provided form key
func (f *FormData) Values(key string) []string {
	if values := f.form.Value[key]; len(values) > 0 {
		return values
	}
	return nil
}

// FirstValue returns the first value for the provided form key
func (f *FormData) FirstValue(key string) string {
	values := f.Values(key)
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func parseMultipartForm(server uploadServer, sizeLimit int64) (*multipart.Form, error) {
	md, _ := metadata.FromIncomingContext(server.Context())
	boundary, err := parseBoundary(md)
	if err != nil {
		return nil, err
	}

	reader := multipart.NewReader(newUploadServerReader(server, sizeLimit), boundary)
	return reader.ReadForm(maxMemory)
}

func parseBoundary(md metadata.MD) (string, error) {
	contentType := pick(md, "grpcgateway-content-type")
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
