package main

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	gatewayfile "github.com/black-06/grpc-gateway-file"
	"github.com/black-06/grpc-gateway-file/examples/proto"
)

func main() {
	grpcAddr, gatewayAddr := ":7070", ":8080"

	// grpc server
	grpcServer := grpc.NewServer()
	proto.RegisterServiceServer(grpcServer, &Service{})
	listener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen %s failed, err: %v", grpcAddr, err)
	}
	go func() {
		log.Fatalf(grpcServer.Serve(listener).Error())
	}()

	// grpc gateway server
	mux := runtime.NewServeMux(
		gatewayfile.WithFileIncomingHeaderMatcher(),
		gatewayfile.WithFileForwardResponseOption(),
		gatewayfile.WithHTTPBodyMarshaler(),
	)
	conn, err := grpc.DialContext(context.Background(), grpcAddr, grpc.WithBlock(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial grpc %s failed, err: %v", grpcAddr, err)
	}
	err = proto.RegisterServiceHandler(context.Background(), mux, conn)
	if err != nil {
		log.Fatalf("register gateway failed, err: %v", err)
	}
	log.Fatalf(http.ListenAndServe(gatewayAddr, mux).Error())
}
