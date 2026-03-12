package workos

import (
	"fmt"
	"log"

	authworkos "github.com/accretional/runrpc/auth/workos"
	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
	"google.golang.org/grpc"
)

// Token validates an access token supplied as Identity.secret against
// the WorkOS JWKS and returns a Resource on success.
type Token struct {
	Client *authworkos.Client
}

func (f *Token) Match(first *identifier.Identity) bool {
	return first.GetSecret() != ""
}

func (f *Token) Handle(first *identifier.Identity, stream grpc.BidiStreamingServer[identifier.Identity, filer.Resource]) error {
	token := first.GetSecret()

	claims, err := f.Client.VerifyAccessToken(token)
	if err != nil {
		return fmt.Errorf("token verification failed: %w", err)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return fmt.Errorf("token has no sub claim")
	}

	log.Printf("[authflows/workos] token verified for sub=%s", sub)

	user, err := f.Client.GetUser(sub)
	if err != nil {
		return fmt.Errorf("fetching user %s: %w", sub, err)
	}

	res := &filer.Resource{
		Id:   0,
		Name: user.Email,
		Type: "identity.authenticated",
		Owners: []*filer.Resource{
			{Name: sub, Type: "workos.user_id"},
		},
	}

	if err := stream.Send(res); err != nil {
		return fmt.Errorf("sending auth resource: %w", err)
	}

	log.Printf("[authflows/workos] authenticated %s (%s) via token", user.Email, sub)
	return nil
}
