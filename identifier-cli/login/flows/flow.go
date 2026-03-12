package flows

import (
	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
	"google.golang.org/grpc"
)

// ClientFlow drives one authentication exchange from the client side
// of an Identifier.Authenticate bidi stream.
type ClientFlow interface {
	// Run executes the flow and returns the final authenticated Resource.
	Run(stream grpc.BidiStreamingClient[identifier.Identity, filer.Resource]) (*filer.Resource, error)
}
