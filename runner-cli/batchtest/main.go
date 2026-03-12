// batchtest: client that calls Runner.Batch on a running runner-cli.
//
// Usage:
//   runner-cli -listen :9092 &
//   batchtest [addr]
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/accretional/runrpc/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	log.SetFlags(log.Ltime)

	addr := "localhost:9092"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	dir, err := os.MkdirTemp("", "batchtest-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	inputPath := filepath.Join(dir, "input.txt")
	outputPath := filepath.Join(dir, "output.txt")

	if err := os.WriteFile(inputPath, []byte("What I learned in boating school is\n"), 0644); err != nil {
		log.Fatal(err)
	}

	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer cc.Close()

	client := runner.NewRunnerClient(cc)

	pipe := &runner.Pipe{
		InputPath:  inputPath,
		OutputPath: outputPath,
		Pipeline: []*descriptorpb.MethodDescriptorProto{
			{Name: strPtr("Babble")},
			{Name: strPtr("AppendAndPrint")},
			{Name: strPtr("AddNewLine")},
		},
	}

	log.Println("Batch: Babble → AppendAndPrint → AddNewLine")
	_, err = client.Batch(context.Background(), pipe)
	if err != nil {
		log.Fatalf("Batch: %v", err)
	}

	out, err := os.ReadFile(outputPath)
	if err != nil {
		log.Fatalf("read output: %v", err)
	}
	fmt.Printf("%s", out)
	log.Printf("done (%d bytes)", len(out))
}

func strPtr(s string) *string { return &s }
