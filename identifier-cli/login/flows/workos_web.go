package flows

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"runtime"
	"time"

	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
	"google.golang.org/grpc"
)

// WorkOSWeb runs the AuthKit web signup/login flow via device
// authorization, then sends the resulting access token to the server.
type WorkOSWeb struct {
	Client *DeviceAuthClient
}

func (f *WorkOSWeb) Run(stream grpc.BidiStreamingClient[identifier.Identity, filer.Resource]) (*filer.Resource, error) {
	// Step 1: Request device authorization.
	da, err := f.Client.RequestDeviceAuthorization()
	if err != nil {
		return nil, fmt.Errorf("device authorization: %w", err)
	}

	// Open browser and show fallback URL.
	fmt.Println("  Opening browser to complete sign-in...")
	if err := openBrowserURL(da.VerificationURIComplete); err != nil {
		log.Printf("  Could not open browser: %v", err)
	}
	fmt.Printf("\n  If your browser didn't open, visit:\n")
	fmt.Printf("    %s\n\n", da.VerificationURIComplete)
	fmt.Printf("  Or go to %s and enter code: %s\n\n", da.VerificationURI, da.UserCode)

	// Step 2: Poll until the user completes login.
	interval := time.Duration(da.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(da.ExpiresIn) * time.Second)

	fmt.Print("  Waiting for login")
	var tokenResp *DeviceTokenResponse
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		fmt.Print(".")

		tokenResp, err = f.Client.PollDeviceToken(da.DeviceCode)
		if err != nil {
			fmt.Println()
			return nil, fmt.Errorf("device auth: %w", err)
		}
		if tokenResp != nil {
			break
		}
	}
	fmt.Println()

	if tokenResp == nil {
		return nil, fmt.Errorf("sign-in timed out")
	}

	fmt.Printf("  Signed in as %s\n\n", tokenResp.User.Email)

	// Step 3: Send the access token to the server.
	fmt.Println("  Sending credentials to server...")
	err = stream.Send(&identifier.Identity{
		Name: tokenResp.User.Email,
		Provider: &identifier.Identity_Secret{
			Secret: tokenResp.AccessToken,
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

func openBrowserURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}
