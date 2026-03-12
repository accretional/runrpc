package main

import (
	"github.com/accretional/runrpc/commander"
	"github.com/accretional/runrpc/identifier"
	"github.com/accretional/runrpc/images"
	"github.com/accretional/runrpc/loader"
	"github.com/accretional/runrpc/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func registerServices(srv *grpc.Server) {
	loader.RegisterLoaderServer(srv, loader.NewLoaderServer())
	commander.RegisterCommanderServer(srv, commander.NewCommanderServer())
	images.RegisterImagerServer(srv, images.NewImagerServer())
	runner.RegisterRunnerServer(srv, runner.NewRunnerServer())
	identifier.RegisterIdentifierServer(srv, identifier.NewIdentifierServer())
	reflection.Register(srv)
}
