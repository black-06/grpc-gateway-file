package gateway_file

import (
	"io"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/pkg/errors"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/encoding/protojson"
)

func WithHTTPBodyMarshaler() runtime.ServeMuxOption {
	return runtime.WithMarshalerOption("*", &HTTPBodyMarshaler{
		HTTPBodyMarshaler: &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions:   protojson.MarshalOptions{EmitUnpopulated: true},
				UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
			},
		},
	})
}

// HTTPBodyMarshaler is the same as runtime.HTTPBodyMarshaler.
// It adds HttpBodyDecoder for HttpBody stream and provide the Delimiter as empty.
type HTTPBodyMarshaler struct {
	*runtime.HTTPBodyMarshaler
}

func (m *HTTPBodyMarshaler) NewDecoder(body io.Reader) runtime.Decoder {
	return &HttpBodyDecoder{
		Decoder: m.Marshaler.NewDecoder(body),
		body:    body,
		buf:     make([]byte, defaultBufSize),
		eof:     false,
	}
}

func (m *HTTPBodyMarshaler) Delimiter() []byte { return []byte{} }

type HttpBodyDecoder struct {
	runtime.Decoder

	body io.Reader
	buf  []byte
	eof  bool
}

func (decoder *HttpBodyDecoder) Decode(v any) error {
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
	return errors.WithStack(err)
}
