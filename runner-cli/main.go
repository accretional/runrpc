package main

import (
	"flag"
	"io"
	"log"
	"net"
	"sync"

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
		ec := &exitOnClose{Conn: conn, closed: make(chan struct{})}
		go srv.Serve(newSingleConnListener(ec))
		// Block until the parent closes the socketpair.
		<-ec.closed
		// All RPCs are done (parent closed after receiving responses).
		srv.GracefulStop()
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

// exitOnClose wraps a net.Conn and signals when a Read returns an error.
type exitOnClose struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *exitOnClose) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		c.once.Do(func() { close(c.closed) })
	}
	return n, err
}

type singleConnListener struct{ ch chan net.Conn }

func newSingleConnListener(conn net.Conn) *singleConnListener {
	l := &singleConnListener{ch: make(chan net.Conn, 1)}
	l.ch <- conn
	return l
}
func (l *singleConnListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, io.EOF
	}
	return c, nil
}
func (l *singleConnListener) Close() error   { close(l.ch); return nil }
func (l *singleConnListener) Addr() net.Addr { return &fdAddr{} }

type fdAddr struct{}

func (a *fdAddr) Network() string { return "fd" }
func (a *fdAddr) String() string  { return "fd:3" }
