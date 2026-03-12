// Package authflow provides integration test scaffolding for the
// Identifier authentication flows.
//
// Tests in this package hit real WorkOS APIs and require:
//
//	WORKOS_API_KEY      – server-side API key (sk_test_...)
//	WORKOS_CLIENT_ID    – application client ID
//	WORKOS_TEST_EMAIL   – email of an existing WorkOS user
//
// Tests are skipped when these are not set.
package authflow

import (
	"net"
	"os"
	"testing"

	"github.com/accretional/runrpc/auth/workos"
	"github.com/accretional/runrpc/identifier"
	authworkos "github.com/accretional/runrpc/identifier/authflows/workos"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Env holds the WorkOS credentials read from the environment.
type Env struct {
	APIKey    string
	ClientID  string
	TestEmail string
}

// LoadEnv reads credentials from environment variables and skips the
// test if any are missing.
func LoadEnv(t *testing.T) Env {
	t.Helper()
	e := Env{
		APIKey:    os.Getenv("WORKOS_API_KEY"),
		ClientID:  os.Getenv("WORKOS_CLIENT_ID"),
		TestEmail: os.Getenv("WORKOS_TEST_EMAIL"),
	}
	if e.APIKey == "" || e.ClientID == "" || e.TestEmail == "" {
		t.Skip("WORKOS_API_KEY, WORKOS_CLIENT_ID, and WORKOS_TEST_EMAIL must be set")
	}
	return e
}

// ServerConfig controls how the test server is set up.
type ServerConfig struct {
	// WorkOS client for server-side flows. Nil means Authenticate is
	// unimplemented.
	WorkOS *workos.Client
	// ServiceName is the authority name returned by the server.
	ServiceName string
}

// TestServer is a running gRPC server with the Identifier service.
type TestServer struct {
	Addr   string
	Server *grpc.Server
}

// StartServer launches a gRPC server on a random port with the
// Identifier service configured per cfg. The server is stopped when
// the test completes.
func StartServer(t *testing.T, cfg ServerConfig) *TestServer {
	t.Helper()

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()

	svcName := cfg.ServiceName
	if svcName == "" {
		svcName = "test.example.com"
	}

	var idOpts []identifier.Option
	idOpts = append(idOpts, identifier.WithAuthority(svcName, "test-owner"))
	if cfg.WorkOS != nil {
		idOpts = append(idOpts, identifier.WithAuthHandler(&identifier.AuthDispatcher{
			Flows: []identifier.AuthFlow{
				&authworkos.Token{Client: cfg.WorkOS},
				&authworkos.Invite{Client: cfg.WorkOS, ServiceName: svcName},
			},
		}))
	}
	identifier.RegisterIdentifierServer(srv, identifier.NewIdentifierServer(idOpts...))

	go srv.Serve(lis)
	t.Cleanup(func() { srv.GracefulStop() })

	return &TestServer{Addr: lis.Addr().String(), Server: srv}
}

// DialServer creates a gRPC client connection to the test server.
// The connection is closed when the test completes.
func DialServer(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// NewWorkOSClient creates a workos.Client from the test env. Fails the
// test on error.
func NewWorkOSClient(t *testing.T, env Env) *workos.Client {
	t.Helper()
	c, err := workos.NewClient(env.APIKey, env.ClientID)
	if err != nil {
		t.Fatalf("workos.NewClient: %v", err)
	}
	return c
}

// ObtainTestToken gets a valid access token for the test email by
// creating a magic auth code and immediately verifying it. This
// exercises real WorkOS APIs.
func ObtainTestToken(t *testing.T, c *workos.Client, email string) string {
	t.Helper()

	ma, err := c.SendMagicAuth(email)
	if err != nil {
		t.Fatalf("SendMagicAuth(%s): %v", email, err)
	}

	resp, err := c.AuthenticateMagicAuth(email, ma.Code)
	if err != nil {
		t.Fatalf("AuthenticateMagicAuth: %v", err)
	}

	if resp.AccessToken == "" {
		t.Fatal("AuthenticateMagicAuth returned empty access token")
	}
	return resp.AccessToken
}
