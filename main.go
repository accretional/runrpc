package main

import (
	"log"
	"net"
	"os"

	"github.com/accretional/runrpc/commander"
	"github.com/accretional/runrpc/loader"
	"github.com/accretional/runrpc/runner"
	"google.golang.org/grpc"
)

func main() {
	// Create a listener on stdin (file descriptor 0)
	// We use FileListener to wrap stdin as a listener
	listener, err := net.FileListener(os.Stdin)
	if err != nil {
		log.Fatalf("Failed to create listener from stdin: %v", err)
	}

	// Create a new gRPC server
	grpcServer := grpc.NewServer()

	// Register all three services
	loader.RegisterLoaderServer(grpcServer, loader.NewLoaderServer())
	commander.RegisterCommanderServer(grpcServer, commander.NewCommanderServer())
	runner.RegisterRunnerServer(grpcServer, runner.NewRunnerServer())

	log.Println("gRPC server starting on stdin...")

	// Start serving
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
