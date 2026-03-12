package main

import (
	"github.com/accretional/runrpc/commander"
	"github.com/accretional/runrpc/loader"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func registerServices(srv *grpc.Server) {
	loader.RegisterLoaderServer(srv, loader.NewLoaderServer())
	commander.RegisterCommanderServer(srv, commander.NewCommanderServer())
	reflection.Register(srv)
}
