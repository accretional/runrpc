package commander

import (
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
	return status.Error(codes.Unimplemented, "Shell not yet implemented")
}
