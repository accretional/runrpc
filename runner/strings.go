package runner

import (
	"io"
	"math/rand"
	"strings"

	"google.golang.org/grpc"
)

// Babble takes a string and creates 50 permutations of its words.
func (s *runnerServer) Babble(req *StringValue, stream grpc.ServerStreamingServer[StringValue]) error {
	for _, perm := range babbleStrings(req.Value, 50) {
		if err := stream.Send(&StringValue{Value: perm}); err != nil {
			return err
		}
	}
	return nil
}

// AppendAndPrint adds a random character to the end of each string (bidi streaming).
func (s *runnerServer) AppendAndPrint(stream grpc.BidiStreamingServer[StringValue, StringValue]) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&StringValue{Value: appendRandomChar(msg.Value)}); err != nil {
			return err
		}
	}
}

// AddNewLine collects all strings and joins them with newlines.
func (s *runnerServer) AddNewLine(stream grpc.ClientStreamingServer[StringValue, StringValue]) error {
	var lines []string
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&StringValue{Value: strings.Join(lines, "\n")})
		}
		if err != nil {
			return err
		}
		lines = append(lines, msg.Value)
	}
}

// babbleStrings returns n random permutations of the words in s.
func babbleStrings(s string, n int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		perm := make([]string, len(words))
		copy(perm, words)
		rand.Shuffle(len(perm), func(a, b int) { perm[a], perm[b] = perm[b], perm[a] })
		out[i] = strings.Join(perm, " ")
	}
	return out
}

const randChars = "abcdefghijklmnopqrstuvwxyz0123456789!@#$%"

// appendRandomChar appends a random character to s.
func appendRandomChar(s string) string {
	return s + string(randChars[rand.Intn(len(randChars))])
}
