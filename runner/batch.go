package runner

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Batch orchestrates a streaming pipeline through forked children.
//
// For pipeline [M1, M2, M3] with input_path and output_path:
//   - Create a named pipe (FIFO)
//   - Fork a child, call Batch on it with {input: fifo, pipeline: [M2, M3], output: output_path}
//   - Read from input_path, apply M1, write results to the FIFO
//   - Child recursively does the same for M2, M3, ...
//   - The last child reads from its FIFO, applies its method, writes to output_path
//
// Data flows through the pipe chain without buffering in the parent.
func (s *runnerServer) Batch(ctx context.Context, pipe *Pipe) (*emptypb.Empty, error) {
	if len(pipe.Pipeline) == 0 {
		return nil, status.Error(codes.InvalidArgument, "pipeline cannot be empty")
	}

	method := pipe.Pipeline[0]
	remaining := pipe.Pipeline[1:]

	log.Printf("[batch pid %d] method=%s, %d remaining, input=%s output=%s",
		os.Getpid(), method.GetName(), len(remaining), pipe.InputPath, pipe.OutputPath)

	// Determine where this stage writes its output.
	var outputPath string
	var fifoPath string
	var childCC *grpc.ClientConn
	var childPid int
	var childErr chan error

	if len(remaining) > 0 {
		// Create a FIFO for the next stage.
		dir, err := os.MkdirTemp("", "batch-pipe-*")
		if err != nil {
			return nil, status.Errorf(codes.Internal, "mktemp: %v", err)
		}
		defer os.RemoveAll(dir)

		fifoPath = filepath.Join(dir, "pipe")
		if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
			return nil, status.Errorf(codes.Internal, "mkfifo: %v", err)
		}
		outputPath = fifoPath

		// Fork child and call Batch on it with remaining pipeline.
		var err2 error
		childCC, childPid, err2 = forkAndDial()
		if err2 != nil {
			return nil, status.Errorf(codes.Internal, "fork: %v", err2)
		}

		// Call child.Batch in a goroutine — it blocks until the child
		// finishes processing. Meanwhile we write to the FIFO.
		childErr = make(chan error, 1)
		go func() {
			childClient := NewRunnerClient(childCC)
			_, err := childClient.Batch(ctx, &Pipe{
				InputPath:  fifoPath,
				Pipeline:   remaining,
				OutputPath: pipe.OutputPath,
			})
			childErr <- err
		}()
	} else {
		// Last stage: write directly to output_path.
		outputPath = pipe.OutputPath
	}

	// Read from input, apply method, write to output.
	err := applyMethodToFile(s, method, pipe.InputPath, outputPath)

	// If we have a child, close the FIFO (EOF to child) and wait.
	if childCC != nil {
		// Wait for child's Batch to complete.
		if cerr := <-childErr; cerr != nil && err == nil {
			err = status.Errorf(codes.Internal, "child: %v", cerr)
		}
		childCC.Close()
		waitForPid(childPid)
		log.Printf("[batch pid %d] child %d exited", os.Getpid(), childPid)
	}

	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// applyMethodToFile reads lines from inputPath, applies the named method,
// and writes result lines to outputPath.
func applyMethodToFile(s *runnerServer, method *descriptorpb.MethodDescriptorProto, inputPath, outputPath string) error {
	inFile, err := os.Open(inputPath)
	if err != nil {
		return status.Errorf(codes.NotFound, "open input %s: %v", inputPath, err)
	}
	defer inFile.Close()

	outFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return status.Errorf(codes.Internal, "open output %s: %v", outputPath, err)
	}
	defer outFile.Close()

	scanner := bufio.NewScanner(inFile)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return status.Errorf(codes.Internal, "read: %v", err)
	}

	name := method.GetName()
	switch name {
	case "Babble":
		// Each input line → 50 permutations.
		for _, line := range lines {
			perms := babbleStrings(line, 50)
			for _, p := range perms {
				fmt.Fprintln(outFile, p)
			}
		}
	case "AppendAndPrint":
		// Each line → line + random char.
		for _, line := range lines {
			fmt.Fprintln(outFile, appendRandomChar(line))
		}
	case "AddNewLine":
		// All lines → joined with newlines (single output).
		fmt.Fprintln(outFile, strings.Join(lines, "\n"))
	default:
		return status.Errorf(codes.InvalidArgument, "unknown method %q", name)
	}

	return nil
}

// forkAndDial forks the current executable with fd 3 socketpair and
// returns a gRPC client connection to the child.
func forkAndDial() (*grpc.ClientConn, int, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, 0, err
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, 0, err
	}
	syscall.CloseOnExec(fds[0])

	pid, err := syscall.ForkExec(self, os.Args, &syscall.ProcAttr{
		Env: os.Environ(),
		Files: []uintptr{
			os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd(),
			uintptr(fds[1]),
		},
	})
	syscall.Close(fds[1])
	if err != nil {
		syscall.Close(fds[0])
		return nil, 0, err
	}

	f := os.NewFile(uintptr(fds[0]), "parent-end")
	parentConn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		syscall.Kill(pid, syscall.SIGKILL)
		return nil, 0, err
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
		return nil, 0, err
	}

	return cc, pid, nil
}

func waitForPid(pid int) {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(pid, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		return
	}
}
