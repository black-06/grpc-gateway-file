# gRPC-Gateway-file

Are you confused about how to integrate file upload and download into gRPC-Gateway?

Try gRPC-Gateway-file, it is a plugin of the [gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway).

It allows you to define upload and download api in the gRPC service proto file.

# Feat

- For upload, you no longer have to
  manually [add routes to the mux](https://grpc-ecosystem.github.io/grpc-gateway/docs/mapping/binary_file_uploads/)
- For download, It supports Resume Transfer Protocol.
- Best of all, you can implement them directly in gRPC service.

## Usage

0. Get it.

```shell
go get -u github.com/black-06/grpc-gateway-file
```

1. Defining gRPC proto file.  
   The result of download api, and the request of upload api, must be "stream google.api.HttpBody"

```protobuf
import "google/api/annotations.proto";
import "google/api/httpbody.proto";

service Service {
  rpc DownloadFile (XXX) returns (stream google.api.HttpBody) {
    option (google.api.http) = { get: "/api/file/download" };
  };

  rpc UploadFile (stream google.api.HttpBody) returns (XXX) {
    option (google.api.http) = { post: "/api/file/upload", body: "*" };
  };
}
```

2. Generate golang code as usual.
3. Using gRPC-Gateway-file in gRPC-Gateway.

```go
import gatewayfile "github.com/black-06/grpc-gateway-file"

mux := runtime.NewServeMux(
	gatewayfile.WithFileIncomingHeaderMatcher(),
	gatewayfile.WithFileForwardResponseOption(),
	gatewayfile.WithHTTPBodyMarshaler(),
)
```

4. Done, enjoy it

A more complete example is [here](./examples)

## Known issues

HTTPBodyMarshaler will change the Delimiter of all server-stream to empty. 

Be careful if you have other server streams api. 

More context see https://github.com/grpc-ecosystem/grpc-gateway/issues/2557 
