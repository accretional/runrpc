package workos

import (
	"fmt"
	"io"
	"log"

	authworkos "github.com/accretional/runrpc/auth/workos"
	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
	"google.golang.org/grpc"
)

// Invite handles the multi-step invitation flow:
//
//  1. Client sends Identity with name (display name)
//  2. Server responds with service info resource, prompting for email
//  3. Client sends Identity with id=email
//  4. Server sends invitation/magic-auth, responds with status
//  5. Client sends Identity with secret=code
//  6. Server verifies code, returns authenticated Resource
type Invite struct {
	Client      *authworkos.Client
	ServiceName string
}

func (f *Invite) Match(first *identifier.Identity) bool {
	return first.GetSecret() == ""
}

func (f *Invite) Handle(first *identifier.Identity, stream grpc.BidiStreamingServer[identifier.Identity, filer.Resource]) error {
	displayName := first.GetName()
	if displayName == "" {
		displayName = first.GetId()
	}

	log.Printf("[authflows/workos/invite] new login from %q", displayName)

	// Step 1: Send service info + email prompt.
	svcInfo := &filer.Resource{
		Name: f.ServiceName,
		Type: "identity.service_info",
		Owners: []*filer.Resource{
			{Name: "email_required", Type: "identity.prompt"},
		},
	}
	if err := stream.Send(svcInfo); err != nil {
		return fmt.Errorf("sending service info: %w", err)
	}

	// Step 2: Receive email.
	emailMsg, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("receiving email: %w", err)
	}

	email := emailMsg.GetId()
	if email == "" {
		email = emailMsg.GetName()
	}
	if email == "" {
		return fmt.Errorf("client did not provide an email")
	}

	log.Printf("[authflows/workos/invite] %q provided email %s", displayName, email)

	// Step 3: Send invitation or magic auth.
	_, invErr := f.Client.CreateInvitation(email)
	if invErr != nil {
		log.Printf("[authflows/workos/invite] invitation failed (%v), sending magic auth", invErr)
		if _, err := f.Client.SendMagicAuth(email); err != nil {
			return fmt.Errorf("sending magic auth to %s: %w", email, err)
		}
	}

	// Tell the client we sent a code.
	statusRes := &filer.Resource{
		Name: email,
		Type: "identity.code_sent",
		Owners: []*filer.Resource{
			{Name: "code_required", Type: "identity.prompt"},
		},
	}
	if err := stream.Send(statusRes); err != nil {
		return fmt.Errorf("sending code_sent status: %w", err)
	}

	// Step 4: Receive code.
	codeMsg, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("receiving code: %w", err)
	}

	code := codeMsg.GetSecret()
	if code == "" {
		return fmt.Errorf("client did not provide a code")
	}

	log.Printf("[authflows/workos/invite] verifying code for %s", email)

	// Step 5: Verify the code via WorkOS authenticate.
	authResp, err := f.Client.AuthenticateMagicAuth(email, code)
	if err != nil {
		return fmt.Errorf("code verification for %s: %w", email, err)
	}

	log.Printf("[authflows/workos/invite] authenticated %s (%s)", email, authResp.User.ID)

	// Step 6: Return authenticated resource.
	res := &filer.Resource{
		Id:   0,
		Name: authResp.User.Email,
		Type: "identity.authenticated",
		Owners: []*filer.Resource{
			{Name: authResp.User.ID, Type: "workos.user_id"},
		},
	}
	if err := stream.Send(res); err != nil {
		return fmt.Errorf("sending auth resource: %w", err)
	}

	authworkos.StoreUser(authResp.User.ID, &authworkos.UserState{
		User: authResp.User,
	})

	return nil
}
