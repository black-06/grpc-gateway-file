package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	gatewayfile "github.com/black-06/grpc-gateway-file"
	"github.com/black-06/grpc-gateway-file/examples/proto"
)

type Service struct {
	*proto.UnimplementedServiceServer
}

func (*Service) DownloadFile(_ *proto.DownloadFileRequest, server proto.Service_DownloadFileServer) error {
	// contentType is not necessary.
	// gatewayfile.ServeFile(server, "", "/the/file/path")
	return gatewayfile.ServeFile(server, "application/octet-stream", "/the/file/path")
}

func (*Service) UploadFile(server proto.Service_UploadFileServer) error {
	header, err := gatewayfile.MultipartFormHeader(server, "file")
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid file")
	}

	file, err := header.Open()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid file")
	}

	// do something with upload file, for example, calc MD5
	hash := md5.New()
	if _, err = io.Copy(hash, file); err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}
	fmt.Println(hex.EncodeToString(hash.Sum(nil)))

	// Of course, it can also be saved.
	// gatewayfile.SaveMultipartFile(header, "/to/save/path")

	// Don't forget to close the server.
	return server.SendAndClose(&emptypb.Empty{})
}
