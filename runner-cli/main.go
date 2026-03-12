package main

import (
	"flag"
	"io"
	"log"
	"net"

	"github.com/accretional/runrpc/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var flagListen = flag.String("listen", ":9092", "TCP address to listen on")

func main() {
	flag.Parse()

	srv := grpc.NewServer()
	runner.RegisterRunnerServer(srv, runner.NewRunnerServer())
	reflection.Register(srv)

	// If we were forked, fd 3 is a socketpair to the parent.
	// Serve gRPC over it instead of binding a port.
	if conn, err := runner.InheritedConn(); err == nil {
		log.Printf("runner-cli: forked child, serving on inherited fd 3")
		lis := newSingleConnListener(conn)
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("Server error: %v", err)
		}
		return
	}

	// Normal mode: listen on a TCP port.
	lis, err := net.Listen("tcp", *flagListen)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", *flagListen, err)
	}

	log.Printf("runner-cli listening on %s", *flagListen)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// singleConnListener implements net.Listener for a single connection.
type singleConnListener struct {
	conn chan net.Conn
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	l := &singleConnListener{conn: make(chan net.Conn, 1)}
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
	return &fdAddr{}
}

type fdAddr struct{}

func (a *fdAddr) Network() string { return "fd" }
func (a *fdAddr) String() string  { return "fd:3" }
