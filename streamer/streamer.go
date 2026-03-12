package streamer

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type streamerServer struct {
	UnimplementedStreamerServer
	srv *grpc.Server // the gRPC server this is registered on
}

func NewStreamerServer() *streamerServer {
	return &streamerServer{}
}

// SetServer gives the streamer a handle to its own gRPC server,
// so Pipeline can dispatch methods via reflection on registered services.
func (s *streamerServer) SetServer(srv *grpc.Server) {
	s.srv = srv
}

func (s *streamerServer) Increment(ctx context.Context, req *anypb.Any) (*anypb.Any, error) {
	var val wrapperspb.Int64Value
	if err := req.UnmarshalTo(&val); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "unpack Int64Value: %v", err)
	}
	val.Value++
	out, err := anypb.New(&val)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "repack: %v", err)
	}
	return out, nil
}

func (s *streamerServer) Pipeline(stream grpc.BidiStreamingServer[PipelineMessage, PipelineMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	config := first.GetConfig()
	if config == nil {
		return status.Error(codes.InvalidArgument, "first message must be PipelineConfig")
	}
	if config.Method == nil || config.Method.GetName() == "" {
		return status.Error(codes.InvalidArgument, "config must include a method descriptor")
	}

	var pipeErr error
	if config.Depth <= 0 {
		pipeErr = s.pipelineLeaf(stream, config.Method)
	} else {
		pipeErr = s.pipelineRelay(stream, config)
	}

	// Pipeline is done. Shut down this server so the process can exit
	// and the parent's Wait4 returns.
	if s.srv != nil {
		go s.srv.GracefulStop()
	}
	return pipeErr
}

// pipelineLeaf: no more forks. Apply the method to each value and return it.
func (s *streamerServer) pipelineLeaf(
	stream grpc.BidiStreamingServer[PipelineMessage, PipelineMessage],
	method *descriptorpb.MethodDescriptorProto,
) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		val := msg.GetValue()
		if val == nil {
			continue
		}
		result, err := s.invokeMethod(method, val)
		if err != nil {
			return err
		}
		if err := stream.Send(&PipelineMessage{Msg: &PipelineMessage_Value{Value: result}}); err != nil {
			return err
		}
	}
}

// pipelineRelay: fork a child, open a Pipeline stream to it, relay values through.
func (s *streamerServer) pipelineRelay(
	stream grpc.BidiStreamingServer[PipelineMessage, PipelineMessage],
	config *PipelineConfig,
) error {
	childCC, cleanup, err := ForkAndDial()
	if err != nil {
		return status.Errorf(codes.Internal, "fork: %v", err)
	}
	defer cleanup()

	child := NewStreamerClient(childCC)
	downstream, err := child.Pipeline(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "child pipeline: %v", err)
	}

	// Send config to child with depth-1.
	err = downstream.Send(&PipelineMessage{
		Msg: &PipelineMessage_Config{Config: &PipelineConfig{
			Method: config.Method,
			Depth:  config.Depth - 1,
		}},
	})
	if err != nil {
		return status.Errorf(codes.Internal, "child config: %v", err)
	}

	// downstream→upstream relay goroutine.
	errc := make(chan error, 1)
	go func() {
		for {
			msg, err := downstream.Recv()
			if err == io.EOF {
				errc <- nil
				return
			}
			if err != nil {
				errc <- fmt.Errorf("downstream recv: %w", err)
				return
			}
			if err := stream.Send(msg); err != nil {
				errc <- fmt.Errorf("upstream send: %w", err)
				return
			}
		}
	}()

	// upstream→downstream: apply method, forward.
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		val := msg.GetValue()
		if val == nil {
			continue
		}
		result, err := s.invokeMethod(config.Method, val)
		if err != nil {
			return err
		}
		if err := downstream.Send(&PipelineMessage{Msg: &PipelineMessage_Value{Value: result}}); err != nil {
			return fmt.Errorf("downstream send: %w", err)
		}
	}

	downstream.CloseSend()
	if err := <-errc; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	return nil
}

// invokeMethod dispatches a unary call described by a MethodDescriptorProto
// on this server's registered gRPC services. It finds the matching handler
// and invokes it directly.
func (s *streamerServer) invokeMethod(method *descriptorpb.MethodDescriptorProto, val *anypb.Any) (*anypb.Any, error) {
	if s.srv == nil {
		return nil, status.Error(codes.Internal, "no gRPC server set on streamer")
	}

	methodName := method.GetName()

	// Walk the registered services to find a matching method.
	for svcName, info := range s.srv.GetServiceInfo() {
		for _, m := range info.Methods {
			if m.Name == methodName {
				// Found it. Build the full method path and invoke via the server's
				// registered handler using a direct Go call.
				// The handler is registered on the server, so we call it in-process
				// by looking up the service implementation.
				return s.callRegisteredMethod(svcName, methodName, val)
			}
		}
	}

	return nil, status.Errorf(codes.NotFound, "method %q not found in registered services", methodName)
}

// callRegisteredMethod invokes a unary method on a registered service.
// It uses the fact that we know our own server implementation.
func (s *streamerServer) callRegisteredMethod(svcName, methodName string, val *anypb.Any) (*anypb.Any, error) {
	// For now: match against known services on this server.
	// This is the bridge between the MethodDescriptorProto and our Go types.
	// Full generic reflection dispatch (using protoreflect + grpc service info)
	// is the end goal.
	fullMethod := "/" + svcName + "/" + methodName
	switch fullMethod {
	case Streamer_Increment_FullMethodName:
		return s.Increment(context.Background(), val)
	default:
		return nil, status.Errorf(codes.Unimplemented, "no handler for %s", fullMethod)
	}
}

// ForkAndDial forks the current executable with a socketpair on fd 3,
// dials the child over gRPC, and returns the connection + a cleanup
// function that closes the connection and waits for the child to exit.
func ForkAndDial() (grpc.ClientConnInterface, func(), error) {
	self, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}

	f := os.NewFile(uintptr(fds[0]), "parent-end")
	parentConn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		syscall.Kill(pid, syscall.SIGKILL)
		return nil, nil, err
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
		return nil, nil, err
	}

	cleanup := func() {
		cc.Close()
		parentConn.Close()
		for {
			var ws syscall.WaitStatus
			_, err := syscall.Wait4(pid, &ws, 0, nil)
			if err == syscall.EINTR {
				continue
			}
			break
		}
		log.Printf("[pid %d] child %d exited", os.Getpid(), pid)
	}

	log.Printf("[pid %d] forked child %d", os.Getpid(), pid)
	return cc, cleanup, nil
}

// InheritedConn returns a net.Conn wrapping fd 3 if it exists.
func InheritedConn() (net.Conn, error) {
	f := os.NewFile(3, "fork-child")
	if f == nil {
		return nil, fmt.Errorf("fd 3 not available")
	}
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("fd 3 is not a socket: %w", err)
	}
	return conn, nil
}
