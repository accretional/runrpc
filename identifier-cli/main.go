package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/accretional/runrpc/identifier-cli/login"
	"github.com/accretional/runrpc/identifier"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	flagServer   = flag.String("server", "localhost:9090", "gRPC server address")
	flagLogin    = flag.Bool("login", false, "Sign in via AuthKit web flow (opens browser)")
	flagClientID = flag.String("workos_client", os.Getenv("WORKOS_CLIENT_ID"), "WorkOS client ID for device auth")
)

func main() {
	flag.Parse()

	serverAddr := *flagServer

	// Step 1: Connect to the server.
	conn, err := grpc.NewClient(serverAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect to %s: %v", serverAddr, err)
	}
	defer conn.Close()

	idClient := identifier.NewIdentifierClient(conn)

	// Step 2: Determine flow.
	var clientID string
	if *flagLogin {
		clientID = *flagClientID
		if clientID == "" {
			fmt.Fprintln(os.Stderr, "WORKOS_CLIENT_ID must be set (or use -workos_client) for -login")
			os.Exit(1)
		}
	}

	// Step 3: Run login flow.
	res, err := login.Run(context.Background(), idClient, serverAddr, "", clientID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		os.Exit(1)
	}

	_ = res
}
