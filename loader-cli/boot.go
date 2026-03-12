package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"

	"github.com/accretional/runrpc/loader"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

func boot() {
	if !flag.Parsed() {
		flag.Parse()
	}
}

func showReflectionServices() {
	serverConn, clientConn, err := socketpair()
	if err != nil {
		log.Fatalf("Failed to create socketpair: %v", err)
	}

	srv := grpc.NewServer()
	registerServices(srv)
	go srv.Serve(newSingleConnListener(serverConn))

	conn, err := grpc.NewClient("passthrough:///",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return clientConn, nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to dial reflection: %v", err)
	}
	defer conn.Close()

	refClient := grpcreflect.NewClientV1Alpha(context.Background(),
		reflectpb.NewServerReflectionClient(conn))
	services, err := refClient.ListServices()
	if err != nil {
		log.Fatalf("Failed to list services: %v", err)
	}
	for _, service := range services {
		os.Stdout.WriteString(service + "\n")
	}
}

func execLoader() {
	loaderSvc := loader.NewLoaderServer()
	positional := flag.Args()
	path := positional[0]
	var args []string
	if len(positional) > 1 {
		args = positional[1:]
	}
	_, err := loaderSvc.Exec(context.Background(), &loader.ExecutionArgs{
		Path: path,
		Args: args,
	})
	if err != nil {
		log.Fatalf("Failed to exec: %v", err)
	}
}
