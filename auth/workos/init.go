// github.com/accretional/runrpc/identifier/workos/init.go
package workos

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

// ---------------------------------------------------------------------------
// CLI flags
// ---------------------------------------------------------------------------

var (
	flagSharedWorkOS      = flag.Bool("shared_workos", false, "Enable a package-level singleton WorkOS client")
	flagWorkOSAccessToken = flag.String("workos_access_token", "", "Pre-existing WorkOS access token to verify")
	flagUserEmail         = flag.String("user_email", "hello@accretional.com", "Fallback email for invitation flow")
	flagWorkOSCLIAuth     = flag.Bool("workos_cli_auth", false, "Run device authorization (CLI auth) flow and block until complete")
)

const defaultUserEmail = "hello@accretional.com"

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client wraps the credentials and cached state needed to talk to WorkOS.
// When shared_workos is true a singleton lives at the package level;
// otherwise callers create their own via NewClient.
type Client struct {
	APIKey   string
	ClientID string

	jwks    jwk.Set
	jwksMu  sync.RWMutex
	jwksURL string

	HTTPClient *http.Client
}

// NewPublicClient builds a Client that only has a client ID (no API key).
// It is suitable for public flows like device authorization where no
// server-side secret is needed. JWKS is still fetched for local token
// verification.
func NewPublicClient(clientID string) (*Client, error) {
	c := &Client{
		ClientID: clientID,
		jwksURL:  fmt.Sprintf("https://api.workos.com/sso/jwks/%s", clientID),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	if err := c.refreshJWKS(); err != nil {
		return nil, fmt.Errorf("jwks initial fetch for client %s: %w", clientID, err)
	}
	return c, nil
}

// NewClient builds a Client from explicit credentials and eagerly fetches
// the JWKS key set so callers get an early error on misconfiguration.
func NewClient(apiKey, clientID string) (*Client, error) {
	c := &Client{
		APIKey:   apiKey,
		ClientID: clientID,
		jwksURL:  fmt.Sprintf("https://api.workos.com/sso/jwks/%s", clientID),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	if err := c.refreshJWKS(); err != nil {
		return nil, fmt.Errorf("jwks initial fetch for client %s: %w", clientID, err)
	}
	return c, nil
}

// refreshJWKS fetches and parses the remote JWKS document.
func (c *Client) refreshJWKS() error {
	set, err := jwk.Fetch(
		context.Background(),
		c.jwksURL,
		jwk.WithHTTPClient(c.HTTPClient),
	)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", c.jwksURL, err)
	}
	if set.Len() == 0 {
		return fmt.Errorf("jwks endpoint %s returned zero keys", c.jwksURL)
	}

	c.jwksMu.Lock()
	c.jwks = set
	c.jwksMu.Unlock()

	log.Printf("[workos] jwks refreshed: %d key(s) from %s", set.Len(), c.jwksURL)
	return nil
}

// VerifyAccessToken validates a JWT string against the cached JWKS and
// returns the parsed claims on success.
func (c *Client) VerifyAccessToken(raw string) (jwt.MapClaims, error) {
	c.jwksMu.RLock()
	keySet := c.jwks
	c.jwksMu.RUnlock()

	if keySet == nil {
		return nil, fmt.Errorf("jwks not loaded")
	}

	token, err := jwt.Parse(raw, func(t *jwt.Token) (interface{}, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("token header missing kid")
		}
		key, found := keySet.LookupKeyID(kid)
		if !found {
			return nil, fmt.Errorf("kid %q not in jwks", kid)
		}
		var rawKey interface{}
		if err := key.Raw(&rawKey); err != nil {
			return nil, fmt.Errorf("extracting raw public key: %w", err)
		}
		return rawKey, nil
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer("https://api.workos.com"),
	)
	if err != nil {
		return nil, fmt.Errorf("jwt verification: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid claims")
	}
	return claims, nil
}

// ---------------------------------------------------------------------------
// Package-level singleton
// ---------------------------------------------------------------------------

var (
	sharedClient   *Client
	sharedClientMu sync.RWMutex
)

// Shared returns the package-level singleton client, or nil when
// shared_workos is disabled or Init has not been called.
func Shared() *Client {
	sharedClientMu.RLock()
	defer sharedClientMu.RUnlock()
	return sharedClient
}

// ---------------------------------------------------------------------------
// Init – main orchestration entry point
// ---------------------------------------------------------------------------

// Init should be called after flag.Parse().  It bootstraps a WorkOS client,
// then either verifies an access token or falls through to the invitation
// flow.
func Init() {
	if !flag.Parsed() {
		flag.Parse()
	}

	client, err := bootstrapClient()
	if err != nil {
		log.Printf("[workos/init] bootstrap failed: %v", err)
		return
	}

	// ── Path A: CLI device auth (blocks until user completes login) ────
	if *flagWorkOSCLIAuth {
		runCLIAuthFlow(client)
		return
	}

	// ── Path B: access token supplied ──────────────────────────────────
	if tok := *flagWorkOSAccessToken; tok != "" {
		runAccessTokenFlow(client, tok)
		return
	}

	// ── Path C: no token – invitation flow ─────────────────────────────
	runInvitationFlow(client)
}

func bootstrapClient() (*Client, error) {
	apiKey := os.Getenv("WORKOS_API_KEY")
	clientID := os.Getenv("WORKOS_CLIENT_ID")
	if apiKey == "" || clientID == "" {
		return nil, fmt.Errorf("WORKOS_API_KEY and WORKOS_CLIENT_ID must be set")
	}

	c, err := NewClient(apiKey, clientID)
	if err != nil {
		return nil, err
	}

	if *flagSharedWorkOS {
		sharedClientMu.Lock()
		sharedClient = c
		sharedClientMu.Unlock()
		log.Println("[workos/init] shared singleton client stored")
	}

	return c, nil
}

// ---------------------------------------------------------------------------
// Flow: access token
// ---------------------------------------------------------------------------

func runAccessTokenFlow(c *Client, token string) {
	claims, err := c.VerifyAccessToken(token)
	if err != nil {
		log.Printf("[workos/init] token verification failed: %v", err)
		return
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		log.Println("[workos/init] token has no sub claim")
		return
	}
	log.Printf("[workos/init] token verified (sub=%s)", sub)

	// Fetch user profile.
	user, err := c.GetUser(sub)
	if err != nil {
		log.Printf("[workos/init] GetUser(%s): %v", sub, err)
		return
	}

	// Fetch identities.
	identities, err := c.ListIdentities(sub)
	if err != nil {
		log.Printf("[workos/init] ListIdentities(%s): %v", sub, err)
		// non-fatal – continue
	}

	// Fetch sessions.
	sessions, err := c.ListSessions(sub)
	if err != nil {
		log.Printf("[workos/init] ListSessions(%s): %v", sub, err)
	}

	// Persist into the package-level user map.
	StoreUser(user.ID, &UserState{
		User:       user,
		Identities: identities,
		Sessions:   sessions,
	})

	// Send magic auth to the user's primary email.
	if user.Email != "" {
		if _, err := c.SendMagicAuth(user.Email); err != nil {
			log.Printf("[workos/init] SendMagicAuth(%s): %v", user.Email, err)
		} else {
			log.Printf("[workos/init] magic auth sent to %s", user.Email)
		}
	}
}

// ---------------------------------------------------------------------------
// Flow: invitation
// ---------------------------------------------------------------------------

func runInvitationFlow(c *Client) {
	email := *flagUserEmail
	isDefault := (email == defaultUserEmail)

	if isDefault {
		// For the default address, try magic auth first (user may already
		// exist), then fall back to invitation.
		if _, err := c.SendMagicAuth(email); err != nil {
			log.Printf("[workos/init] SendMagicAuth(default %s): %v – trying invitation", email, err)
			if _, iErr := c.CreateInvitation(email); iErr != nil {
				log.Printf("[workos/init] CreateInvitation(default %s): %v", email, iErr)
			} else {
				log.Printf("[workos/init] invitation created for default %s", email)
			}
		} else {
			log.Printf("[workos/init] magic auth sent to default %s", email)
		}
		return
	}

	// Non-default email: create invitation, fall back to magic auth if
	// user already exists.
	inv, err := c.CreateInvitation(email)
	if err != nil {
		log.Printf("[workos/init] CreateInvitation(%s): %v – trying magic auth", email, err)
		if _, mErr := c.SendMagicAuth(email); mErr != nil {
			log.Printf("[workos/init] SendMagicAuth(%s): %v", email, mErr)
		} else {
			log.Printf("[workos/init] magic auth sent to %s", email)
		}
		return
	}
	log.Printf("[workos/init] invitation created for %s (id=%s)", email, inv.ID)

	StoreUser(email, &UserState{
		PendingInvitationID: inv.ID,
	})
}

// ---------------------------------------------------------------------------
// Browser helper
// ---------------------------------------------------------------------------

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

// ---------------------------------------------------------------------------
// Flow: CLI device authorization
// ---------------------------------------------------------------------------

func runCLIAuthFlow(c *Client) {
	da, err := c.RequestDeviceAuthorization()
	if err != nil {
		log.Printf("[workos/cli-auth] device authorization request failed: %v", err)
		return
	}

	// Open the browser automatically; print the URL as fallback.
	fmt.Println()
	if err := openBrowser(da.VerificationURIComplete); err != nil {
		log.Printf("[workos/cli-auth] could not open browser: %v", err)
	}
	fmt.Printf("  If your browser didn't open, visit:\n\n")
	fmt.Printf("    %s\n\n", da.VerificationURIComplete)
	fmt.Printf("  Or go to %s and enter code: %s\n\n", da.VerificationURI, da.UserCode)

	interval := time.Duration(da.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(da.ExpiresIn) * time.Second)

	log.Printf("[workos/cli-auth] waiting for user to complete login (expires in %ds)...", da.ExpiresIn)

	for time.Now().Before(deadline) {
		time.Sleep(interval)

		tokenResp, err := c.PollDeviceToken(da.DeviceCode)
		if err != nil {
			log.Printf("[workos/cli-auth] %v", err)
			return
		}
		if tokenResp == nil {
			// authorization_pending or slow_down – keep polling.
			continue
		}

		// Success.
		log.Printf("[workos/cli-auth] authenticated as %s (%s)",
			tokenResp.User.Email, tokenResp.User.ID)

		StoreUser(tokenResp.User.ID, &UserState{
			User:         tokenResp.User,
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
		})

		// If the caller also wants an access-token flow (user profile,
		// identities, sessions), run it with the freshly obtained token.
		if tokenResp.AccessToken != "" {
			runAccessTokenFlow(c, tokenResp.AccessToken)
		}

		fmt.Printf("  Authenticated as %s\n\n", tokenResp.User.Email)
		return
	}

	log.Println("[workos/cli-auth] device code expired before user completed login")
}
