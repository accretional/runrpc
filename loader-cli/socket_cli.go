package main

import (
	"io"
	"net"
	"os"
	"syscall"
)

// socketpair creates an anonymous AF_UNIX stream pair and returns the two
// ends as net.Conn values. The underlying file descriptors are closed once
// wrapped.
func socketpair() (server, client net.Conn, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}

	sf := os.NewFile(uintptr(fds[0]), "server")
	cf := os.NewFile(uintptr(fds[1]), "client")

	server, err = net.FileConn(sf)
	sf.Close()
	if err != nil {
		cf.Close()
		return nil, nil, err
	}

	client, err = net.FileConn(cf)
	cf.Close()
	if err != nil {
		server.Close()
		return nil, nil, err
	}

	return server, client, nil
}

// singleConnListener implements net.Listener for a single connection.
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

type socketpairAddr struct{}

func (a *socketpairAddr) Network() string { return "socketpair" }
func (a *socketpairAddr) String() string  { return "socketpair" }

// isatty checks if a file descriptor is a terminal.
func isatty(fd int) bool {
	return isattyFd(fd)
}
