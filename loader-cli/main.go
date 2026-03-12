package main

import (
	"flag"
	"io"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"
)

var (
	flagListen    = flag.String("listen", "", "TCP address to listen on (e.g. :9090). Overrides stdin/stdout mode.")
	flagSystemOTP = flag.Bool("system_otp", false, "Generate a system OTP token for direct authentication")
)

func main() {
	// Run identifier bootstrap (secrets, container detection, domain verify, WorkOS).
	boot()

	// Non-flag positional arguments (available after flag.Parse in boot).
	args := flag.Args()
	stdinIsTerminal := isatty(0)

	// Exec mode: replace process image with the given command.
	if len(args) > 0 {
		execLoader()
		return // unreachable on success
	}

	// TCP listener mode.
	if *flagListen != "" {
		lis, err := net.Listen("tcp", *flagListen)
		if err != nil {
			log.Fatalf("Failed to listen on %s: %v", *flagListen, err)
		}
		log.Printf("Listening on %s", *flagListen)
		grpcServer := grpc.NewServer()
		registerServices(grpcServer)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Server error: %v", err)
		}
		return
	}

	// Interactive terminal with no args: list registered services and exit.
	if stdinIsTerminal {
		showReflectionServices()
		return
	}

	// gRPC server on stdin/stdout via socketpair.
	serverConn, clientConn, err := socketpair()
	if err != nil {
		log.Fatalf("Failed to create socketpair: %v", err)
	}

	listener := newSingleConnListener(serverConn)
	grpcServer := grpc.NewServer()
	registerServices(grpcServer)

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- grpcServer.Serve(listener)
	}()

	// Bridge stdin/stdout ↔ client side of the socketpair.
	stdinClosed := make(chan struct{})
	firstReadDone := make(chan bool, 1)

	go func() {
		defer close(stdinClosed)
		defer clientConn.Close()

		buf := make([]byte, 4096)
		n, err := os.Stdin.Read(buf)
		firstReadDone <- (n > 0)

		if n > 0 {
			clientConn.Write(buf[:n])
			io.Copy(clientConn, os.Stdin)
		}
		if err != nil && err != io.EOF {
			log.Printf("stdin error: %v", err)
		}
	}()

	go func() {
		io.Copy(os.Stdout, clientConn)
	}()

	hadData := <-firstReadDone
	isPid1 := os.Getpid() == 1

	if !hadData && !isPid1 {
		showReflectionServices()
		return
	}

	if isPid1 {
		<-serveDone
		return
	}

	<-stdinClosed
	grpcServer.GracefulStop()
}
