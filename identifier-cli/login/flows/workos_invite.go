package flows

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
	"google.golang.org/grpc"
)

// WorkOSInvite drives the invitation/magic-auth code flow from the
// client side. It prompts the user for their email, helps them check
// their inbox, and relays the verification code to the server.
type WorkOSInvite struct {
	DefaultName string
}

func (f *WorkOSInvite) Run(stream grpc.BidiStreamingClient[identifier.Identity, filer.Resource]) (*filer.Resource, error) {
	reader := bufio.NewReader(os.Stdin)

	// Step 1: Prompt for display name and send it.
	name := f.DefaultName
	fmt.Printf("  Name [%s]: ", name)
	if line, _ := reader.ReadString('\n'); strings.TrimSpace(line) != "" {
		name = strings.TrimSpace(line)
	}

	if err := stream.Send(&identifier.Identity{Name: name}); err != nil {
		return nil, fmt.Errorf("sending name: %w", err)
	}

	// Step 2: Receive service info / email prompt.
	svcInfo, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("receiving service info: %w", err)
	}
	fmt.Printf("  Service: %s\n", svcInfo.GetName())

	// Step 3: Prompt for email and send it.
	fmt.Print("  Email: ")
	emailLine, _ := reader.ReadString('\n')
	email := strings.TrimSpace(emailLine)
	if email == "" {
		return nil, fmt.Errorf("no email provided")
	}

	if err := stream.Send(&identifier.Identity{Id: email}); err != nil {
		return nil, fmt.Errorf("sending email: %w", err)
	}

	// Step 4: Receive code_sent status.
	statusRes, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("receiving code status: %w", err)
	}

	if statusRes.GetType() == "identity.code_sent" {
		fmt.Printf("  Code sent to %s\n", statusRes.GetName())
	}

	// Help the user check their email.
	suggestMailbox(email, reader)

	// Step 5: Prompt for code and send it.
	fmt.Print("  Enter code: ")
	codeLine, _ := reader.ReadString('\n')
	code := strings.TrimSpace(codeLine)
	if code == "" {
		return nil, fmt.Errorf("no code provided")
	}

	if err := stream.Send(&identifier.Identity{
		Provider: &identifier.Identity_Secret{Secret: code},
	}); err != nil {
		return nil, fmt.Errorf("sending code: %w", err)
	}
	stream.CloseSend()

	// Step 6: Receive authenticated resource.
	res, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("server closed without auth result")
		}
		return nil, fmt.Errorf("receiving auth result: %w", err)
	}

	return res, nil
}

// suggestMailbox offers to open the user's email provider based on
// their email domain.
func suggestMailbox(email string, reader *bufio.Reader) {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return
	}
	domain := strings.ToLower(parts[1])

	var webmailURL string
	switch {
	case domain == "gmail.com":
		webmailURL = "https://mail.google.com"
	case domain == "outlook.com" || domain == "hotmail.com" || domain == "live.com":
		webmailURL = "https://outlook.live.com"
	case domain == "yahoo.com":
		webmailURL = "https://mail.yahoo.com"
	default:
		webmailURL = detectWebmail(domain)
	}

	fmt.Println()
	if webmailURL != "" {
		fmt.Printf("  [o] Open %s in browser\n", webmailURL)
	}
	fmt.Println("  [m] Open default mail app")
	fmt.Println("  [enter] I'll enter the code directly")
	fmt.Print("  > ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToLower(choice))

	switch choice {
	case "o":
		if webmailURL != "" {
			openURL(webmailURL)
		}
	case "m":
		openURL("mailto:")
	default:
		// User will enter code directly — do nothing.
	}
	fmt.Println()
}

// detectWebmail looks up MX records for the domain and returns a
// webmail URL if a well-known provider is found.
func detectWebmail(domain string) string {
	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return ""
	}

	for _, mx := range mxRecords {
		host := strings.ToLower(mx.Host)
		switch {
		case strings.Contains(host, "google") || strings.Contains(host, "gmail"):
			return "https://mail.google.com"
		case strings.Contains(host, "outlook") || strings.Contains(host, "microsoft"):
			return "https://outlook.live.com"
		case strings.Contains(host, "yahoo"):
			return "https://mail.yahoo.com"
		case strings.Contains(host, "proton") || strings.Contains(host, "protonmail"):
			return "https://mail.proton.me"
		case strings.Contains(host, "icloud") || strings.Contains(host, "apple"):
			return "https://www.icloud.com/mail"
		case strings.Contains(host, "zoho"):
			return "https://mail.zoho.com"
		case strings.Contains(host, "fastmail"):
			return "https://www.fastmail.com"
		}
	}
	return ""
}

func openURL(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	}
}
