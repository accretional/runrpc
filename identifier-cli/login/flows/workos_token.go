package flows

import (
	"fmt"
	"io"

	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
	"google.golang.org/grpc"
)

// WorkOSToken sends an existing access token to the server and waits
// for an authenticated Resource back.
type WorkOSToken struct {
	AccessToken string
	UserName    string
}

func (f *WorkOSToken) Run(stream grpc.BidiStreamingClient[identifier.Identity, filer.Resource]) (*filer.Resource, error) {
	// Send our token as a secret.
	err := stream.Send(&identifier.Identity{
		Name: f.UserName,
		Provider: &identifier.Identity_Secret{
			Secret: f.AccessToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("sending token: %w", err)
	}
	stream.CloseSend()

	res, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("server closed without responding")
		}
		return nil, fmt.Errorf("receiving auth result: %w", err)
	}

	return res, nil
}
