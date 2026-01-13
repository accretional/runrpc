package loader

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type loaderServer struct {
	UnimplementedLoaderServer
}

func NewLoaderServer() LoaderServer {
	return &loaderServer{}
}

func (s *loaderServer) Exec(ctx context.Context, args *ExecutionArgs) (*ExitCode, error) {
	if args.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	// Look up the full path to the executable if not an absolute path
	execPath := args.Path
	if execPath[0] != '/' {
		var err error
		execPath, err = lookPath(execPath)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "command not found: %v", err)
		}
	}

	// Convert env map to []string slice in "KEY=VALUE" format
	var envSlice []string
	if len(args.Env) > 0 {
		envSlice = make([]string, 0, len(args.Env))
		for key, value := range args.Env {
			envSlice = append(envSlice, fmt.Sprintf("%s=%s", key, value))
		}
	} else {
		// Inherit parent environment if no env provided
		envSlice = os.Environ()
	}

	// Prepare argv - first arg should be the command name itself
	argv := make([]string, 0, len(args.Args)+1)
	argv = append(argv, args.Path)
	argv = append(argv, args.Args...)

	// Execute the command, replacing the current process
	// Note: This will not return on success, only on error
	err := syscall.Exec(execPath, argv, envSlice)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to exec: %v", err)
	}

	// This line will never be reached if exec succeeds
	return &ExitCode{Code: 0}, nil
}

// lookPath searches for an executable named file in the directories
// named by the PATH environment variable.
func lookPath(file string) (string, error) {
	path := os.Getenv("PATH")
	for _, dir := range splitPath(path) {
		if dir == "" {
			dir = "."
		}
		path := dir + "/" + file
		if err := checkExecutable(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: command not found", file)
}

// splitPath splits a PATH string into directory components
func splitPath(path string) []string {
	if path == "" {
		return []string{}
	}
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == ':' {
			parts = append(parts, path[start:i])
			start = i + 1
		}
	}
	parts = append(parts, path[start:])
	return parts
}

// checkExecutable checks if a file exists and is executable
func checkExecutable(file string) error {
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	// Check if file is executable (at least one execute bit set)
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("permission denied")
	}
	return nil
}

func (s *loaderServer) Link(stream grpc.ClientStreamingServer[BytesValue, LoadHandle]) error {
	return status.Error(codes.Unimplemented, "Link not yet implemented")
}

func (s *loaderServer) Load(ctx context.Context, args *LoadArgs) (*ExitCode, error) {
	return nil, status.Error(codes.Unimplemented, "Load not yet implemented")
}
