// pipeline: streaming increment pipeline over gRPC fork chain.
//
// Root process opens a bidi Pipeline to a forked child chain (depth levels).
// Each level applies the named method (Increment) to every value passing
// through, then forwards downstream. Values stream back when they reach
// the leaf. With depth=5, input 0 becomes 6 (incremented at 6 levels).
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"github.com/accretional/runrpc/streamer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const depth = 5

func main() {
	log.SetFlags(0)

	// Forked child: serve Streamer on fd 3.
	if conn, err := streamer.InheritedConn(); err == nil {
		log.Printf("[pid %d] child serving", os.Getpid())
		srv := grpc.NewServer()
		ss := streamer.NewStreamerServer()
		ss.SetServer(srv)
		streamer.RegisterStreamerServer(srv, ss)
		srv.Serve(singleListener(conn))
		return
	}

	// Root process.
	log.Printf("[pid %d] root: depth=%d", os.Getpid(), depth)

	// We are also a pipeline stage — create our own streamer for local Increment calls.
	ss := streamer.NewStreamerServer()
	// Root doesn't need a gRPC server; it calls Increment directly.

	// Fork first child.
	childCC, cleanup, err := streamer.ForkAndDial()
	if err != nil {
		log.Fatalf("fork: %v", err)
	}
	defer cleanup()

	// Open Pipeline to child chain.
	child := streamer.NewStreamerClient(childCC)
	downstream, err := child.Pipeline(context.Background())
	if err != nil {
		log.Fatalf("pipeline: %v", err)
	}

	// Configure: Increment method, depth-1 remaining forks.
	methodDesc := &descriptorpb.MethodDescriptorProto{
		Name:       strPtr("Increment"),
		InputType:  strPtr(".google.protobuf.Any"),
		OutputType: strPtr(".google.protobuf.Any"),
	}
	err = downstream.Send(&streamer.PipelineMessage{
		Msg: &streamer.PipelineMessage_Config{Config: &streamer.PipelineConfig{
			Method: methodDesc,
			Depth:  depth - 1,
		}},
	})
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Collect results in background.
	type result struct {
		input  int64
		output int64
	}
	results := make(chan result, 10)
	errc := make(chan error, 1)
	go func() {
		for {
			msg, err := downstream.Recv()
			if err == io.EOF {
				close(results)
				errc <- nil
				return
			}
			if err != nil {
				errc <- fmt.Errorf("recv: %w", err)
				close(results)
				return
			}
			var val wrapperspb.Int64Value
			if err := msg.GetValue().UnmarshalTo(&val); err != nil {
				errc <- fmt.Errorf("unpack: %w", err)
				close(results)
				return
			}
			results <- result{output: val.Value}
		}
	}()

	// Send 10 values. Root applies Increment first (root is a pipeline stage too).
	for i := int64(0); i < 10; i++ {
		packed, _ := anypb.New(&wrapperspb.Int64Value{Value: i})
		incremented, err := ss.Increment(context.Background(), packed)
		if err != nil {
			log.Fatalf("root increment: %v", err)
		}
		err = downstream.Send(&streamer.PipelineMessage{
			Msg: &streamer.PipelineMessage_Value{Value: incremented},
		})
		if err != nil {
			log.Fatalf("send %d: %v", i, err)
		}
	}
	downstream.CloseSend()

	// Print results.
	for i := int64(0); i < 10; i++ {
		r, ok := <-results
		if !ok {
			break
		}
		fmt.Printf("input=%d output=%d (expected %d)\n", i, r.output, i+int64(depth)+1)
	}

	if err := <-errc; err != nil {
		log.Fatalf("pipeline error: %v", err)
	}
	log.Printf("[pid %d] done", os.Getpid())
}

func strPtr(s string) *string { return &s }

func dialConn(conn net.Conn) (*grpc.ClientConn, error) {
	return grpc.NewClient("passthrough:///",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return conn, nil
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}

type singleConnListener struct{ ch chan net.Conn }

func singleListener(c net.Conn) net.Listener {
	l := &singleConnListener{ch: make(chan net.Conn, 1)}
	l.ch <- c
	return l
}
func (l *singleConnListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, io.EOF
	}
	return c, nil
}
func (l *singleConnListener) Close() error   { close(l.ch); return nil }
func (l *singleConnListener) Addr() net.Addr { return &fdAddr{} }

type fdAddr struct{}

func (a *fdAddr) Network() string { return "fd" }
func (a *fdAddr) String() string  { return "fd:3" }
