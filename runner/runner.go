package runner

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type runnerServer struct {
	UnimplementedRunnerServer
}

func NewRunnerServer() RunnerServer {
	return &runnerServer{}
}

func (s *runnerServer) Fork(ctx context.Context, req *ForkRequest) (*Process, error) {
	return nil, status.Error(codes.Unimplemented, "Fork not yet implemented")
}

func (s *runnerServer) Spawn(ctx context.Context, req *SpawnRequest) (*Process, error) {
	return nil, status.Error(codes.Unimplemented, "Spawn not yet implemented")
}

func (s *runnerServer) Stop(ctx context.Context, req *StopRequest) (*StopResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Stop not yet implemented")
}
