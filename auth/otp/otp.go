// Package otp provides a simple one-time-password authentication
// provider. It generates (or accepts) a single token and validates it.
//
// The "system" provider is the default: it generates a UUID on creation
// and prints it to stdout so the operator can use it to authenticate.
// This replaces the old root/system secret mechanism.
package otp

import (
	"fmt"
	"log"

	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

// Provider implements both identifier.LoginProvider (for boot-time
// authentication) and identifier.AuthFlow (for Authenticate RPC).
type Provider struct {
	name  string
	token string
}

// NewSystem creates a system OTP provider with a generated token.
// The token is printed to stdout for the operator.
func NewSystem() *Provider {
	token := uuid.NewString()
	fmt.Printf("[otp] system token: %s\n", token)
	return &Provider{name: "system", token: token}
}

// New creates an OTP provider with the given name and token.
func New(name, token string) *Provider {
	return &Provider{name: name, token: token}
}

// Token returns the provider's token value.
func (p *Provider) Token() string { return p.token }

// --- identifier.LoginProvider ---

func (p *Provider) Name() string { return p.name }

// Login returns immediately with a Resource representing the system
// identity. The OTP login provider doesn't block — the token is
// already known.
func (p *Provider) Login() (*filer.Resource, error) {
	return &filer.Resource{
		Name: p.name,
		Type: "identity.system",
	}, nil
}

// --- identifier.AuthFlow ---

// Match returns true when the client's secret matches this provider's
// token exactly.
func (p *Provider) Match(first *identifier.Identity) bool {
	return first.GetSecret() == p.token
}

// Handle validates the token and returns an authenticated Resource.
func (p *Provider) Handle(first *identifier.Identity, stream grpc.BidiStreamingServer[identifier.Identity, filer.Resource]) error {
	log.Printf("[otp] %s authenticated via token", p.name)

	res := &filer.Resource{
		Name: p.name,
		Type: "identity.authenticated",
		Owners: []*filer.Resource{
			{Name: p.name, Type: "otp.provider"},
		},
	}
	return stream.Send(res)
}
