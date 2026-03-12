package identifier

import (
	"fmt"
	"io"
	"log"

	"github.com/accretional/runrpc/filer"
	"google.golang.org/grpc"
)

// AuthFlow handles one authentication exchange on the server side of an
// Identifier.Authenticate bidi stream. Implementations are stateless —
// dispatch is based purely on the content of the first Identity message.
type AuthFlow interface {
	// Match returns true if this flow should handle the given first
	// Identity message from the client.
	Match(first *Identity) bool

	// Handle runs the flow to completion. The first Identity has
	// already been received and is passed in. On success the
	// implementation must send at least one Resource back.
	Handle(first *Identity, stream grpc.BidiStreamingServer[Identity, filer.Resource]) error
}

// LoginProvider authenticates a user during startup (client-initiated).
// Login providers run before the gRPC server starts and may block until
// authentication completes.
type LoginProvider interface {
	// Name returns a human-readable name for this provider.
	Name() string

	// Login blocks until authentication completes or fails.
	Login() (*filer.Resource, error)
}

// AuthDispatcher implements AuthenticateHandler by reading the first
// Identity message and dispatching to the first matching AuthFlow.
type AuthDispatcher struct {
	Flows []AuthFlow
}

// Handle reads the first Identity from the stream and dispatches to
// the appropriate flow.
func (d *AuthDispatcher) Handle(stream grpc.BidiStreamingServer[Identity, filer.Resource]) error {
	first, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("receiving first identity: %w", err)
	}

	for _, f := range d.Flows {
		if f.Match(first) {
			log.Printf("[identifier/auth] dispatching to %T", f)
			return f.Handle(first, stream)
		}
	}

	return fmt.Errorf("no flow matched initial identity (name=%q secret=%v)",
		first.GetName(), first.GetSecret() != "")
}
