package loader

import (
	"context"

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
	return nil, status.Error(codes.Unimplemented, "Exec not yet implemented")
}

func (s *loaderServer) Link(stream grpc.ClientStreamingServer[BytesValue, LoadHandle]) error {
	return status.Error(codes.Unimplemented, "Link not yet implemented")
}

func (s *loaderServer) Load(ctx context.Context, args *LoadArgs) (*ExitCode, error) {
	return nil, status.Error(codes.Unimplemented, "Load not yet implemented")
}
