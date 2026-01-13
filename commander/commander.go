package commander

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type commanderServer struct {
	UnimplementedCommanderServer
}

func NewCommanderServer() CommanderServer {
	return &commanderServer{}
}

func (s *commanderServer) Shell(cmd *Command, stream grpc.ServerStreamingServer[Output]) error {
	if cmd.Command == "" {
		return status.Error(codes.InvalidArgument, "command cannot be empty")
	}

	// Determine which shell to use
	shell := cmd.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	// Working directory
	workDir := cmd.WorkingDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return status.Errorf(codes.Internal, "failed to get working directory: %v", err)
		}
	}

	// Build the command - if args are provided, use them; otherwise execute command as shell script
	var execCmd *exec.Cmd
	if len(cmd.Args) > 0 {
		execCmd = exec.Command(cmd.Command, cmd.Args...)
	} else {
		execCmd = exec.Command(shell, "-c", cmd.Command)
	}

	execCmd.Dir = workDir

	// Set environment
	if len(cmd.Env) > 0 {
		env := make([]string, 0, len(cmd.Env))
		for key, value := range cmd.Env {
			env = append(env, fmt.Sprintf("%s=%s", key, value))
		}
		execCmd.Env = env
	} else {
		execCmd.Env = os.Environ()
	}

	// Create pipes for stdout and stderr
	stdoutPipe, err := execCmd.StdoutPipe()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create stdout pipe: %v", err)
	}
	stderrPipe, err := execCmd.StderrPipe()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create stderr pipe: %v", err)
	}

	// Start the command
	if err := execCmd.Start(); err != nil {
		return status.Errorf(codes.Internal, "failed to start command: %v", err)
	}

	// Stream output in goroutines
	errChan := make(chan error, 2)

	// Stream stdout
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				if sendErr := stream.Send(&Output{
					Stdout: true,
					Data:   buf[:n],
				}); sendErr != nil {
					errChan <- sendErr
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				errChan <- err
				return
			}
		}
		errChan <- nil
	}()

	// Stream stderr
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				if sendErr := stream.Send(&Output{
					Stdout: false,
					Data:   buf[:n],
				}); sendErr != nil {
					errChan <- sendErr
					return
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				errChan <- err
				return
			}
		}
		errChan <- nil
	}()

	// Wait for both streams to finish
	for i := 0; i < 2; i++ {
		if err := <-errChan; err != nil {
			execCmd.Process.Kill()
			return status.Errorf(codes.Internal, "error streaming output: %v", err)
		}
	}

	// Wait for command to complete
	if err := execCmd.Wait(); err != nil {
		return status.Errorf(codes.Internal, "command failed: %v", err)
	}

	return nil
}
