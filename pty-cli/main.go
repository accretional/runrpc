// pty-cli: interactive shell that executes commands via Commander gRPC.
//
// Starts loader-cli as a gRPC server (which registers Commander), then
// reads commands from the user and sends each to Commander.Shell, streaming
// output back to the terminal.
//
// Usage:
//
//	go run ./pty-cli
//	go run ./pty-cli -addr localhost:9090   # connect to existing server
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/accretional/runrpc/commander"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var flagAddr = flag.String("addr", "", "connect to existing gRPC server instead of spawning loader-cli")

func main() {
	log.SetFlags(log.Ltime)
	flag.Parse()

	var cc *grpc.ClientConn
	var cleanup func()

	if *flagAddr != "" {
		var err error
		cc, err = grpc.NewClient(*flagAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatalf("dial %s: %v", *flagAddr, err)
		}
		cleanup = func() { cc.Close() }
	} else {
		var err error
		cc, cleanup, err = spawnLoader()
		if err != nil {
			log.Fatal(err)
		}
	}
	defer cleanup()

	client := commander.NewCommanderClient(cc)

	wd, _ := os.Getwd()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Fprintf(os.Stderr, "pty> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}

		// Handle cd locally.
		if strings.HasPrefix(line, "cd ") {
			dir := strings.TrimSpace(line[3:])
			if dir == "" {
				dir = os.Getenv("HOME")
			}
			if err := os.Chdir(dir); err != nil {
				fmt.Fprintf(os.Stderr, "cd: %v\n", err)
			} else {
				wd, _ = os.Getwd()
			}
			continue
		}

		stream, err := client.Shell(context.Background(), &commander.Command{
			Command:    line,
			WorkingDir: wd,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "shell: %v\n", err)
			continue
		}

		for {
			out, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "recv: %v\n", err)
				break
			}
			if out.Stdout {
				os.Stdout.Write(out.Data)
			} else {
				os.Stderr.Write(out.Data)
			}
		}
	}
}

// spawnLoader starts loader-cli with a socketpair and returns a gRPC connection to it.
func spawnLoader() (*grpc.ClientConn, func(), error) {
	loaderPath, err := findLoader()
	if err != nil {
		return nil, nil, err
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("socketpair: %w", err)
	}
	syscall.CloseOnExec(fds[0])

	// loader-cli checks isatty(0): if stdin is not a terminal it serves
	// gRPC over stdin/stdout. We give it the socketpair on stdin/stdout.
	pid, err := syscall.ForkExec(loaderPath, []string{loaderPath}, &syscall.ProcAttr{
		Env: os.Environ(),
		Files: []uintptr{
			uintptr(fds[1]), // stdin  = our socketpair end
			uintptr(fds[1]), // stdout = same
			os.Stderr.Fd(),  // stderr = pass through
		},
	})
	syscall.Close(fds[1])
	if err != nil {
		syscall.Close(fds[0])
		return nil, nil, fmt.Errorf("forkexec %s: %w", loaderPath, err)
	}

	f := os.NewFile(uintptr(fds[0]), "parent-sock")
	parentConn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		syscall.Kill(pid, syscall.SIGKILL)
		return nil, nil, fmt.Errorf("fileconn: %w", err)
	}

	cc, err := grpc.NewClient("passthrough:///",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return parentConn, nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		parentConn.Close()
		syscall.Kill(pid, syscall.SIGKILL)
		return nil, nil, fmt.Errorf("grpc dial: %w", err)
	}

	cleanup := func() {
		cc.Close()
		parentConn.Close()
		syscall.Kill(pid, syscall.SIGTERM)
		var ws syscall.WaitStatus
		syscall.Wait4(pid, &ws, 0, nil)
	}

	return cc, cleanup, nil
}

func findLoader() (string, error) {
	// Check if loader-cli is built in the project.
	if info, err := os.Stat("loader-cli/loader-cli"); err == nil && !info.IsDir() {
		abs, _ := os.Getwd()
		return abs + "/loader-cli/loader-cli", nil
	}
	// Check PATH.
	if path, err := exec.LookPath("loader-cli"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("loader-cli not found; build it with: go build -o loader-cli/loader-cli ./loader-cli/")
}
