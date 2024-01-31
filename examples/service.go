package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"

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
	fileData, err := gatewayfile.NewFilesData(server)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}

	header := fileData.First("file")
	if header == nil {
		return status.Errorf(codes.InvalidArgument, "missing key file")
	}

	if err = calcFileHash(header); err != nil {
		return status.Errorf(codes.InvalidArgument, err.Error())
	}

	// Of course, it can also be saved.
	// gatewayfile.SaveMultipartFile(header, "/to/save/path")

	// Don't forget to close the server.
	return server.SendAndClose(&emptypb.Empty{})
}

func (*Service) UploadMultipleFiles(server proto.Service_UploadMultipleFilesServer) error {
	fileData, err := gatewayfile.NewFilesData(server)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}

	// one file for one key
	firstHeader := fileData.First("file1")
	if firstHeader == nil {
		return status.Errorf(codes.InvalidArgument, "missing key file1")
	}
	if err = calcFileHash(firstHeader); err != nil {
		return status.Errorf(codes.InvalidArgument, err.Error())
	}

	// multiple files with the same key
	secondHeaders := fileData.Get("file2")
	if secondHeaders == nil {
		return status.Errorf(codes.InvalidArgument, "missing key file2")
	}
	for _, secondHeader := range secondHeaders {
		if err = calcFileHash(secondHeader); err != nil {
			return status.Errorf(codes.InvalidArgument, err.Error())
		}
	}

	return server.SendAndClose(&emptypb.Empty{})
}

func calcFileHash(header *multipart.FileHeader) error {
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()

	hash := md5.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}

	_, _ = fmt.Printf("hash for file %s: %s\n", header.Filename, hex.EncodeToString(hash.Sum(nil)))

	return nil
}
