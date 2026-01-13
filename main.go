package main

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"syscall"
	"unsafe"

	"github.com/accretional/runrpc/commander"
	"github.com/accretional/runrpc/loader"
	"github.com/accretional/runrpc/runner"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

func main() {
	// Check if stdin is a terminal (interactive)
	stdinIsTerminal := isatty(0)

	// If command-line arguments are provided, use Loader.Exec to execute them
	if len(os.Args) > 1 {
		loaderSvc := loader.NewLoaderServer()

		// First argument is the command to execute
		path := os.Args[1]
		// Remaining arguments are passed to the command
		args := []string{}
		if len(os.Args) > 2 {
			args = os.Args[2:]
		}

		// Create ExecutionArgs with empty env map (will inherit from parent)
		execArgs := &loader.ExecutionArgs{
			Path: path,
			Args: args,
			Env:  nil,
		}

		// Execute the command (this will replace the current process)
		_, err := loaderSvc.Exec(context.Background(), execArgs)
		if err != nil {
			log.Fatalf("Failed to exec: %v", err)
		}
		// This line will never be reached if exec succeeds
		return
	}

	// If stdin is a terminal and no args, show reflection output and exit
	if stdinIsTerminal {
		showReflectionServices()
		return
	}

	// No args provided - start gRPC server
	// Create an anonymous Unix socketpair for bidirectional communication
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		log.Fatalf("Failed to create socketpair: %v", err)
	}

	// Create net.Conn from the socket file descriptors
	serverFile := os.NewFile(uintptr(fds[0]), "server")
	clientFile := os.NewFile(uintptr(fds[1]), "client")

	serverConn, err := net.FileConn(serverFile)
	if err != nil {
		log.Fatalf("Failed to create server connection: %v", err)
	}
	serverFile.Close()

	clientConn, err := net.FileConn(clientFile)
	if err != nil {
		log.Fatalf("Failed to create client connection: %v", err)
	}
	clientFile.Close()

	// Create a listener that accepts a single connection
	listener := newSingleConnListener(serverConn)

	// Create a new gRPC server
	grpcServer := grpc.NewServer()

	// Register all three services
	loader.RegisterLoaderServer(grpcServer, loader.NewLoaderServer())
	commander.RegisterCommanderServer(grpcServer, commander.NewCommanderServer())
	runner.RegisterRunnerServer(grpcServer, runner.NewRunnerServer())

	// Register reflection service for grpcurl
	reflection.Register(grpcServer)

	// Start serving in background
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- grpcServer.Serve(listener)
	}()

	// Bridge stdin/stdout to the client connection
	stdinClosed := make(chan struct{})
	stdoutClosed := make(chan struct{})
	firstReadDone := make(chan bool, 1)

	// Copy stdin -> clientConn
	go func() {
		defer close(stdinClosed)
		defer clientConn.Close()

		buf := make([]byte, 4096)
		n, err := os.Stdin.Read(buf)

		// Signal if we got data on first read
		firstReadDone <- (n > 0)

		if n > 0 {
			clientConn.Write(buf[:n])
			io.Copy(clientConn, os.Stdin)
		}

		if err != nil && err != io.EOF {
			log.Printf("stdin error: %v", err)
		}
	}()

	// Copy clientConn -> stdout
	go func() {
		defer close(stdoutClosed)
		io.Copy(os.Stdout, clientConn)
	}()

	// Wait to see if stdin had data on first read
	hadData := <-firstReadDone
	isPid1 := os.Getpid() == 1

	// If no stdin data and not PID 1, list services via reflection and exit
	if !hadData && !isPid1 {
		showReflectionServices()
		return
	}

	// If PID 1, serve indefinitely
	if isPid1 {
		<-serveDone
		return
	}

	// Otherwise serve until stdin closes
	<-stdinClosed
	grpcServer.GracefulStop()
}

// isatty checks if a file descriptor is a terminal
func isatty(fd int) bool {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}

// showReflectionServices lists all gRPC services via reflection
func showReflectionServices() {
	// Create a temporary server for reflection
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		log.Fatalf("Failed to create socketpair: %v", err)
	}
	sf := os.NewFile(uintptr(fds[0]), "client")
	cf := os.NewFile(uintptr(fds[1]), "server")
	clientC, _ := net.FileConn(sf)
	serverC, _ := net.FileConn(cf)
	sf.Close()
	cf.Close()

	srv := grpc.NewServer()
	loader.RegisterLoaderServer(srv, loader.NewLoaderServer())
	commander.RegisterCommanderServer(srv, commander.NewCommanderServer())
	runner.RegisterRunnerServer(srv, runner.NewRunnerServer())
	reflection.Register(srv)
	go srv.Serve(newSingleConnListener(serverC))

	conn, err := grpc.NewClient("passthrough:///",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return clientC, nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err == nil {
		defer conn.Close()
		refClient := grpcreflect.NewClientV1Alpha(context.Background(), reflectpb.NewServerReflectionClient(conn))
		if services, err := refClient.ListServices(); err == nil {
			for _, service := range services {
				os.Stdout.WriteString(service + "\n")
			}
		}
	}
}

// singleConnListener implements net.Listener for a single connection
type singleConnListener struct {
	conn chan net.Conn
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	l := &singleConnListener{
		conn: make(chan net.Conn, 1),
	}
	l.conn <- conn
	return l
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	conn, ok := <-l.conn
	if !ok {
		return nil, io.EOF
	}
	return conn, nil
}

func (l *singleConnListener) Close() error {
	close(l.conn)
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return &socketpairAddr{}
}

// socketpairAddr implements net.Addr for socketpair
type socketpairAddr struct{}

func (a *socketpairAddr) Network() string {
	return "socketpair"
}

func (a *socketpairAddr) String() string {
	return "socketpair"
}
