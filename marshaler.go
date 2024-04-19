package gatewayfile

import (
	"errors"
	"io"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/encoding/protojson"
)

// WithHTTPBodyMarshaler returns a ServeMuxOption which associates inbound and outbound Marshalers to a MIME type in mux.
func WithHTTPBodyMarshaler() runtime.ServeMuxOption {
	return runtime.WithMarshalerOption("multipart/form-data", &httpBodyMarshaler{
		HTTPBodyMarshaler: &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions:   protojson.MarshalOptions{EmitUnpopulated: true},
				UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
			},
		},
	})
}

// httpBodyMarshaler is the same as runtime.httpBodyMarshaler.
// It adds HttpBodyDecoder for HttpBody stream and provide the Delimiter as empty.
type httpBodyMarshaler struct {
	*runtime.HTTPBodyMarshaler
}

func (m *httpBodyMarshaler) NewDecoder(body io.Reader) runtime.Decoder {
	return &httpBodyDecoder{
		Decoder: m.Marshaler.NewDecoder(body),
		body:    body,
		buf:     make([]byte, defaultBufSize),
		eof:     false,
	}
}

func (m *httpBodyMarshaler) Delimiter() []byte { return []byte{} }

type httpBodyDecoder struct {
	runtime.Decoder

	body io.Reader
	buf  []byte
	eof  bool
}

func (decoder *httpBodyDecoder) Decode(v any) error {
	body, ok := v.(*httpbody.HttpBody)
	if !ok {
		// it falls back to the json Decoder.
		return decoder.Decoder.Decode(v)
	}

	if decoder.eof {
		return io.EOF
	}

	n, err := io.ReadFull(decoder.body, decoder.buf)
	if n > 0 {
		body.Data = decoder.buf[:n]
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		decoder.eof = true
		return nil
	}
	return err
}
