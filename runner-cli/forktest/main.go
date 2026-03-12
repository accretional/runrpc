// forktest: recursive fork over gRPC socketpairs, 5 deep.
//
// Each process:
//  1. If fd 3 exists, serve Runner gRPC on it.
//  2. If remaining > 0, fork a child (socketpair, fd 3), dial the
//     child over gRPC, call Stop("ping") as proof-of-life to verify
//     the gRPC link works, then wait for the child to exit.
//  3. If remaining == 0 (leaf), serve until parent disconnects, then exit.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"

	"github.com/accretional/runrpc/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	log.SetFlags(0)

	remaining := 5
	if v := os.Getenv("FORK_REMAINING"); v != "" {
		remaining, _ = strconv.Atoi(v)
	}

	fmt.Printf("[pid %d] remaining=%d\n", os.Getpid(), remaining)

	// If we have fd 3, serve Runner on it so our parent can talk to us.
	if conn, err := runner.InheritedConn(); err == nil {
		ec := &exitOnClose{Conn: conn, closed: make(chan struct{})}
		srv := grpc.NewServer()
		runner.RegisterRunnerServer(srv, runner.NewRunnerServer())
		go srv.Serve(singleListener(ec))

		if remaining <= 0 {
			fmt.Printf("[pid %d] leaf!\n", os.Getpid())
			// Block until the parent closes the socketpair.
			<-ec.closed
			return
		}
	} else if remaining <= 0 {
		fmt.Printf("[pid %d] leaf!\n", os.Getpid())
		return
	}

	// Fork a child with remaining-1.
	childPid, parentConn := forkChild(remaining - 1)
	fmt.Printf("[pid %d] forked child %d\n", os.Getpid(), childPid)

	// Dial the child over gRPC via the socketpair.
	cc, err := grpc.NewClient("passthrough:///",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return parentConn, nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("[pid %d] grpc.NewClient: %v", os.Getpid(), err)
	}

	// Prove gRPC works over the socketpair by making a Runner.Stop call.
	// The process doesn't exist so we get NotFound — that's the proof.
	client := runner.NewRunnerClient(cc)
	_, err = client.Stop(context.Background(), &runner.StopRequest{ProcessId: "ping"})
	if err != nil {
		fmt.Printf("[pid %d] gRPC to child %d OK (got expected error: %v)\n", os.Getpid(), childPid, err)
	} else {
		fmt.Printf("[pid %d] gRPC to child %d OK (unexpected success)\n", os.Getpid(), childPid)
	}

	cc.Close()
	parentConn.Close() // Close the socketpair so the child's server sees EOF.

	// Wait for child to exit.
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(childPid, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		break
	}
	fmt.Printf("[pid %d] child %d done.\n", os.Getpid(), childPid)
}

// exitOnClose wraps a net.Conn and signals when a Read returns an error
// (i.e., the remote end closed the connection).
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

func forkChild(remaining int) (int, net.Conn) {
	self, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		log.Fatal(err)
	}
	syscall.CloseOnExec(fds[0])

	env := setEnv(os.Environ(), "FORK_REMAINING", strconv.Itoa(remaining))

	pid, err := syscall.ForkExec(self, os.Args, &syscall.ProcAttr{
		Env: env,
		Files: []uintptr{
			os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd(),
			uintptr(fds[1]),
		},
	})
	syscall.Close(fds[1])
	if err != nil {
		syscall.Close(fds[0])
		log.Fatal(err)
	}

	f := os.NewFile(uintptr(fds[0]), "parent-end")
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		syscall.Kill(pid, syscall.SIGKILL)
		log.Fatal(err)
	}
	return pid, conn
}

func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

type singleConnListener struct{ ch chan net.Conn }

func singleListener(c net.Conn) net.Listener {
	l := &singleConnListener{ch: make(chan net.Conn, 1)}
	l.ch <- c
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
