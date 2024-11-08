package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
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

const maxDataSize = 1024 * 1024 * 100 // 100MB

func (*Service) UploadFile(server proto.Service_UploadFileServer) error {
	formData, err := gatewayfile.NewFormData(server, maxDataSize)
	if err != nil {
		if errors.Is(err, gatewayfile.ErrSizeLimitExceeded) {
			return status.Errorf(codes.InvalidArgument, "size limit exceeded")
		}

		return status.Errorf(codes.Internal, err.Error())
	}
	// Clean up all temporary form data files after processing completes.
	defer formData.RemoveAll()

	fileHeader := formData.FirstFile("key1")
	if fileHeader == nil {
		return status.Errorf(codes.InvalidArgument, "missing file for key key1")
	}

	if err = calcFileHash(fileHeader); err != nil {
		return status.Errorf(codes.InvalidArgument, err.Error())
	}

	// Of course, it can also be saved.
	// gatewayfile.SaveMultipartFile(header, "/to/save/path")

	// Don't forget to close the server.
	return server.SendAndClose(&emptypb.Empty{})
}

func (*Service) UploadMultipleFiles(server proto.Service_UploadMultipleFilesServer) error {
	formData, err := gatewayfile.NewFormData(server, maxDataSize)
	if err != nil {
		if errors.Is(err, gatewayfile.ErrSizeLimitExceeded) {
			return status.Errorf(codes.InvalidArgument, "size limit exceeded")
		}

		return status.Errorf(codes.Internal, err.Error())
	}
	// Clean up all temporary form data files after processing completes.
	defer formData.RemoveAll()

	// one file for one key
	firstFileHeader := formData.FirstFile("key1")
	if firstFileHeader == nil {
		return status.Errorf(codes.InvalidArgument, "missing file for key key1")
	}
	if err = calcFileHash(firstFileHeader); err != nil {
		return status.Errorf(codes.InvalidArgument, err.Error())
	}

	// multiple files with the same key
	secondFileHeaders := formData.Files("key2")
	if secondFileHeaders == nil {
		return status.Errorf(codes.InvalidArgument, "missing files for key key2")
	}
	for _, secondHeader := range secondFileHeaders {
		if err = calcFileHash(secondHeader); err != nil {
			return status.Errorf(codes.InvalidArgument, err.Error())
		}
	}

	// values
	values := formData.Values("key1")
	for _, value := range values {
		_, _ = fmt.Printf("value for key1: %s\n", value)
	}

	return server.SendAndClose(&emptypb.Empty{})
}

func calcFileHash(fileHeader *multipart.FileHeader) error {
	file, err := fileHeader.Open()
	if err != nil {
		return err
	}
	defer file.Close()

	hash := md5.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}

	_, _ = fmt.Printf("hash for file %s: %s\n", fileHeader.Filename, hex.EncodeToString(hash.Sum(nil)))

	return nil
}

func (*Service) UploadToAnotherService(server proto.Service_UploadToAnotherServiceServer) error {
	// Imagine that the need to upload files to S3 without saving them locally or in memory.

	s3client := &s3ClientMock{}

	if err := gatewayfile.ProcessMultipartUpload(server, func(part *multipart.Part) error {
		_, err := s3client.PutObject(server.Context(), &PutObjectInput{
			Bucket: stringPtr("bucket-name"),
			Key:    stringPtr(part.FileName()),
			Body:   part, // part implements io.Reader interface
		})

		return err
	}, maxDataSize); err != nil {
		if errors.Is(err, gatewayfile.ErrSizeLimitExceeded) {
			return status.Errorf(codes.InvalidArgument, "size limit exceeded")
		}

		return status.Errorf(codes.Internal, err.Error())
	}

	return server.SendAndClose(&emptypb.Empty{})
}

// mock S3 client
type s3ClientMock struct{}

func (*s3ClientMock) PutObject(_ context.Context, put *PutObjectInput) (*PutObjectOutput, error) {
	data, err := io.ReadAll(put.Body)
	if err != nil {
		return nil, err
	}

	_, _ = fmt.Printf("uploading file %s to bucket %s, size: %d\n", *put.Key, *put.Bucket, len(data))
	return &PutObjectOutput{}, nil
}

type PutObjectInput struct {
	Bucket *string
	Key    *string
	Body   io.Reader
}

type PutObjectOutput struct{}

func stringPtr(s string) *string {
	return &s
}
