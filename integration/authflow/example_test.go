package authflow_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/accretional/runrpc/auth/workos"
	"github.com/accretional/runrpc/identifier"
	"github.com/accretional/runrpc/integration/authflow"
)

// TestAuthority verifies that the Authority RPC returns expected server
// identity information.
func TestAuthority(t *testing.T) {
	env := authflow.LoadEnv(t)
	wc := authflow.NewWorkOSClient(t, env)

	tests := []struct {
		name        string
		serviceName string
		wantName    string
	}{
		{"default", "", "test.example.com"},
		{"custom", "myservice.io", "myservice.io"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := authflow.StartServer(t, authflow.ServerConfig{
				WorkOS:      wc,
				ServiceName: tt.serviceName,
			})
			conn := authflow.DialServer(t, srv.Addr)
			client := identifier.NewIdentifierClient(conn)

			resp, err := client.Authority(context.Background(), &identifier.Identity{})
			if err != nil {
				t.Fatalf("Authority: %v", err)
			}
			if resp.GetName() == "" {
				t.Error("Authority returned empty name")
			}
		})
	}
}

// TestTokenFlow verifies that a valid access token is accepted and an
// invalid one is rejected.
func TestTokenFlow(t *testing.T) {
	env := authflow.LoadEnv(t)
	wc := authflow.NewWorkOSClient(t, env)

	tests := []struct {
		name      string
		token     string
		wantError bool
	}{
		{
			name:      "valid_token",
			token:     "", // filled below
			wantError: false,
		},
		{
			name:      "invalid_token",
			token:     "not.a.real.jwt",
			wantError: true,
		},
		{
			name:      "empty_token_enters_invite",
			token:     "",
			wantError: false, // empty secret matches invite flow, which sends service_info
		},
	}

	// Get a real token for the valid case.
	validToken := authflow.ObtainTestToken(t, wc, env.TestEmail)
	tests[0].token = validToken

	srv := authflow.StartServer(t, authflow.ServerConfig{WorkOS: wc})
	conn := authflow.DialServer(t, srv.Addr)
	client := identifier.NewIdentifierClient(conn)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			stream, err := client.Authenticate(ctx)
			if err != nil {
				t.Fatalf("open stream: %v", err)
			}

			err = stream.Send(&identifier.Identity{
				Name: "test-user",
				Provider: &identifier.Identity_Secret{
					Secret: tt.token,
				},
			})
			if err != nil {
				if tt.wantError {
					return
				}
				t.Fatalf("send: %v", err)
			}
			stream.CloseSend()

			res, err := stream.Recv()
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error, got resource: %v", res)
				}
				return
			}
			if err != nil {
				t.Fatalf("recv: %v", err)
			}

			// Empty token enters invite flow — first response is service_info.
			if tt.name == "empty_token_enters_invite" {
				if res.GetType() != "identity.service_info" {
					t.Errorf("type = %q, want identity.service_info", res.GetType())
				}
				t.Logf("invite flow entered, got service_info: %s", res.GetName())
				return
			}

			if res.GetType() != "identity.authenticated" {
				t.Errorf("type = %q, want identity.authenticated", res.GetType())
			}
			if res.GetName() == "" {
				t.Error("authenticated resource has empty name")
			}
			t.Logf("authenticated as %s", res.GetName())
		})
	}
}

// TestInviteFlow exercises the full invitation/magic-auth code flow
// against real WorkOS APIs.
func TestInviteFlow(t *testing.T) {
	env := authflow.LoadEnv(t)
	wc := authflow.NewWorkOSClient(t, env)

	srv := authflow.StartServer(t, authflow.ServerConfig{
		WorkOS:      wc,
		ServiceName: "invite-test.example.com",
	})
	conn := authflow.DialServer(t, srv.Addr)
	client := identifier.NewIdentifierClient(conn)

	tests := []struct {
		name      string
		userName  string
		email     string
		codeFunc  func(t *testing.T) string // returns the code to send
		wantError bool
	}{
		{
			name:     "valid_code",
			userName: "Test User",
			email:    env.TestEmail,
			codeFunc: func(t *testing.T) string {
				// The server will send its own magic auth. We need
				// to get the code it sent. Since SendMagicAuth
				// invalidates previous codes, we pre-create one and
				// race slightly — but WorkOS allows the most recent
				// code, which is what the server sends. We'll get
				// the code from the server's call by making our own
				// call right before the stream starts.
				//
				// Actually, the server sends magic auth when it
				// receives the email. We can't intercept it. Instead,
				// we send our own magic auth AFTER the server sends
				// its, which invalidates the server's code. That's
				// also wrong.
				//
				// The correct approach: pre-create a magic auth code,
				// know it, then when the server also creates one,
				// use the LAST code that was created. Since we can't
				// know the server's code, let's use a different
				// approach: call the API after the server sends
				// code_sent to create a fresh code that we control.
				// This new code is the latest and will work.
				ma, err := wc.SendMagicAuth(env.TestEmail)
				if err != nil {
					t.Fatalf("SendMagicAuth for test: %v", err)
				}
				return ma.Code
			},
		},
		{
			name:     "bad_code",
			userName: "Bad Code User",
			email:    env.TestEmail,
			codeFunc: func(t *testing.T) string {
				return "000000"
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			stream, err := client.Authenticate(ctx)
			if err != nil {
				t.Fatalf("open stream: %v", err)
			}

			// Step 1: Send name.
			if err := stream.Send(&identifier.Identity{Name: tt.userName}); err != nil {
				t.Fatalf("send name: %v", err)
			}

			// Step 2: Receive service info.
			svcInfo, err := stream.Recv()
			if err != nil {
				t.Fatalf("recv service info: %v", err)
			}
			if svcInfo.GetType() != "identity.service_info" {
				t.Fatalf("expected service_info, got %s", svcInfo.GetType())
			}
			t.Logf("service: %s", svcInfo.GetName())

			// Step 3: Send email.
			if err := stream.Send(&identifier.Identity{Id: tt.email}); err != nil {
				t.Fatalf("send email: %v", err)
			}

			// Step 4: Receive code_sent.
			codeSent, err := stream.Recv()
			if err != nil {
				t.Fatalf("recv code_sent: %v", err)
			}
			if codeSent.GetType() != "identity.code_sent" {
				t.Fatalf("expected code_sent, got %s", codeSent.GetType())
			}
			t.Logf("code sent to %s", codeSent.GetName())

			// Get the code to send (may call WorkOS API).
			code := tt.codeFunc(t)

			// Step 5: Send code.
			if err := stream.Send(&identifier.Identity{
				Provider: &identifier.Identity_Secret{Secret: code},
			}); err != nil {
				t.Fatalf("send code: %v", err)
			}

			// Step 6: Receive result.
			res, err := stream.Recv()
			if tt.wantError {
				if err == nil {
					// Might get an error on next recv instead.
					_, err2 := stream.Recv()
					if err2 == nil {
						t.Error("expected error, got success")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("recv result: %v", err)
			}

			if res.GetType() != "identity.authenticated" {
				t.Errorf("type = %q, want identity.authenticated", res.GetType())
			}
			t.Logf("authenticated as %s", res.GetName())
		})
	}
}

// TestNoWorkOS verifies that Authenticate returns Unimplemented when
// no WorkOS client is configured.
func TestNoWorkOS(t *testing.T) {
	srv := authflow.StartServer(t, authflow.ServerConfig{})
	conn := authflow.DialServer(t, srv.Addr)
	client := identifier.NewIdentifierClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Authenticate(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	err = stream.Send(&identifier.Identity{Name: "test"})
	if err != nil {
		// May fail on send or recv.
		return
	}

	_, err = stream.Recv()
	if err == nil {
		t.Error("expected error when WorkOS not configured")
	}
	t.Logf("got expected error: %v", err)
}

// TestDeviceAuthRequest verifies that the device authorization endpoint
// responds (public client, no API key needed).
func TestDeviceAuthRequest(t *testing.T) {
	env := authflow.LoadEnv(t)

	// Use a public client (no API key) — this is what the CLI does.
	c := &workos.Client{
		ClientID: env.ClientID,
	}

	da, err := c.RequestDeviceAuthorization()
	if err != nil {
		t.Fatalf("RequestDeviceAuthorization: %v", err)
	}

	if da.DeviceCode == "" {
		t.Error("empty device code")
	}
	if da.UserCode == "" {
		t.Error("empty user code")
	}
	if da.VerificationURI == "" {
		t.Error("empty verification URI")
	}
	if da.VerificationURIComplete == "" {
		t.Error("empty verification URI complete")
	}
	t.Logf("device auth: user_code=%s uri=%s", da.UserCode, da.VerificationURIComplete)
}

// TestMagicAuthRoundtrip verifies that creating a magic auth code and
// authenticating with it produces a valid access token.
func TestMagicAuthRoundtrip(t *testing.T) {
	env := authflow.LoadEnv(t)
	wc := authflow.NewWorkOSClient(t, env)

	ma, err := wc.SendMagicAuth(env.TestEmail)
	if err != nil {
		t.Fatalf("SendMagicAuth: %v", err)
	}
	if ma.Code == "" {
		t.Fatal("empty code")
	}
	t.Logf("magic auth id=%s code=%s", ma.ID, ma.Code)

	resp, err := wc.AuthenticateMagicAuth(env.TestEmail, ma.Code)
	if err != nil {
		t.Fatalf("AuthenticateMagicAuth: %v", err)
	}
	if resp.AccessToken == "" {
		t.Error("empty access token")
	}
	if resp.User == nil {
		t.Error("nil user")
	} else {
		t.Logf("authenticated user: %s (%s)", resp.User.Email, resp.User.ID)
	}
}

// TestPollDeviceTokenPending verifies that polling a fresh device code
// returns pending (nil, nil) rather than an error.
func TestPollDeviceTokenPending(t *testing.T) {
	env := authflow.LoadEnv(t)

	c := &workos.Client{
		ClientID: env.ClientID,
	}

	da, err := c.RequestDeviceAuthorization()
	if err != nil {
		t.Fatalf("RequestDeviceAuthorization: %v", err)
	}

	resp, err := c.PollDeviceToken(da.DeviceCode)
	if err != nil {
		t.Fatalf("PollDeviceToken returned error: %v", err)
	}
	if resp != nil {
		t.Error("expected nil (pending), got token response")
	}
	t.Log("poll returned pending as expected")
}

// helper to drain stream errors
func drainStream(stream identifier.Identifier_AuthenticateClient) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
