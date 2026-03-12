// github.com/accretional/runrpc/identifier/workos/api.go
package workos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const baseURL = "https://api.workos.com"

// ---------------------------------------------------------------------------
// Low-level HTTP helpers
// ---------------------------------------------------------------------------

// doJSON executes an HTTP request, checks for a 2xx status, and decodes
// the response body into dst (when dst is non-nil).
func (c *Client) doJSON(method, path string, body interface{}, dst interface{}) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, truncate(respBytes, 512))
	}

	if dst != nil {
		if err := json.Unmarshal(respBytes, dst); err != nil {
			return fmt.Errorf("decoding response from %s: %w", path, err)
		}
	}
	return nil
}

// truncate returns at most n bytes of b as a string (for error messages).
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// ---------------------------------------------------------------------------
// User management
// ---------------------------------------------------------------------------

// GetUser fetches a single user by ID.
//
//	GET /user_management/users/{id}
func (c *Client) GetUser(userID string) (*User, error) {
	var u User
	err := c.doJSON("GET", "/user_management/users/"+url.PathEscape(userID), nil, &u)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// listResponse is the generic envelope WorkOS uses for paginated
// collections.
type listResponse[T any] struct {
	Data     []T `json:"data"`
	Metadata struct {
		After  string `json:"after,omitempty"`
		Before string `json:"before,omitempty"`
	} `json:"list_metadata"`
}

// ---------------------------------------------------------------------------
// Identities
// ---------------------------------------------------------------------------

// ListIdentities returns every federated identity linked to the user.
//
//	GET /user_management/users/{id}/identities
func (c *Client) ListIdentities(userID string) ([]Identity, error) {
	// This endpoint returns a bare array, not the paginated envelope.
	var identities []Identity
	err := c.doJSON("GET",
		"/user_management/users/"+url.PathEscape(userID)+"/identities",
		nil, &identities,
	)
	if err != nil {
		return nil, err
	}
	return identities, nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// ListSessions returns active sessions for the given user.
//
//	GET /user_management/users/{id}/sessions
func (c *Client) ListSessions(userID string) ([]Session, error) {
	var resp listResponse[Session]
	err := c.doJSON("GET",
		"/user_management/users/"+url.PathEscape(userID)+"/sessions",
		nil, &resp,
	)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ---------------------------------------------------------------------------
// Magic Auth
// ---------------------------------------------------------------------------

type magicAuthRequest struct {
	Email string `json:"email"`
}

// MagicAuthResponse is the result of creating a magic auth session.
type MagicAuthResponse struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Code   string `json:"code"`
}

// SendMagicAuth triggers a magic-auth email to the given address and
// returns the session details (including the code, useful for testing).
//
//	POST /user_management/magic_auth
func (c *Client) SendMagicAuth(email string) (*MagicAuthResponse, error) {
	var resp MagicAuthResponse
	err := c.doJSON("POST",
		"/user_management/magic_auth",
		&magicAuthRequest{Email: email},
		&resp,
	)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// AuthenticateMagicAuthResponse holds the result of verifying a magic
// auth code.
type AuthenticateMagicAuthResponse struct {
	User         *User  `json:"user"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type authenticateMagicAuthRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	GrantType    string `json:"grant_type"`
	Code         string `json:"code"`
	Email        string `json:"email"`
}

// AuthenticateMagicAuth verifies a magic auth code for the given email.
//
//	POST /user_management/authenticate
func (c *Client) AuthenticateMagicAuth(email, code string) (*AuthenticateMagicAuthResponse, error) {
	var resp AuthenticateMagicAuthResponse
	err := c.doJSON("POST",
		"/user_management/authenticate",
		&authenticateMagicAuthRequest{
			ClientID:     c.ClientID,
			ClientSecret: c.APIKey,
			GrantType:    "urn:workos:oauth:grant-type:magic-auth:code",
			Code:         code,
			Email:        email,
		},
		&resp,
	)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// Invitations
// ---------------------------------------------------------------------------

type createInvitationRequest struct {
	Email string `json:"email"`
}

// CreateInvitation sends a new invitation to the given email.
//
//	POST /user_management/invitations
func (c *Client) CreateInvitation(email string) (*Invitation, error) {
	var inv Invitation
	err := c.doJSON("POST",
		"/user_management/invitations",
		&createInvitationRequest{Email: email},
		&inv,
	)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// ListInvitations returns invitations matching the given email.
//
//	GET /user_management/invitations?email={email}
func (c *Client) ListInvitations(email string) ([]Invitation, error) {
	var resp listResponse[Invitation]
	err := c.doJSON("GET",
		"/user_management/invitations?email="+url.QueryEscape(email),
		nil, &resp,
	)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ResendInvitation re-sends an existing invitation by its ID.
//
//	POST /user_management/invitations/{id}/resend
func (c *Client) ResendInvitation(invitationID string) error {
	return c.doJSON("POST",
		"/user_management/invitations/"+url.PathEscape(invitationID)+"/resend",
		nil, nil,
	)
}

// ---------------------------------------------------------------------------
// Device Authorization (CLI Auth)
// ---------------------------------------------------------------------------

// DeviceAuthorization holds the response from the device authorization
// endpoint.
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceAuthRequest struct {
	ClientID string `json:"client_id"`
}

// RequestDeviceAuthorization initiates the device authorization flow.
//
//	POST /user_management/authorize/device
func (c *Client) RequestDeviceAuthorization() (*DeviceAuthorization, error) {
	var da DeviceAuthorization
	err := c.doJSON("POST",
		"/user_management/authorize/device",
		&deviceAuthRequest{ClientID: c.ClientID},
		&da,
	)
	if err != nil {
		return nil, err
	}
	return &da, nil
}

// DeviceTokenResponse holds the successful authentication result from
// polling the token endpoint.
type DeviceTokenResponse struct {
	User                 *User  `json:"user"`
	OrganizationID       string `json:"organization_id,omitempty"`
	AccessToken          string `json:"access_token"`
	RefreshToken         string `json:"refresh_token"`
	AuthenticationMethod string `json:"authentication_method,omitempty"`
}

type deviceTokenRequest struct {
	GrantType  string `json:"grant_type"`
	DeviceCode string `json:"device_code"`
	ClientID   string `json:"client_id"`
}

// deviceTokenError is the JSON body returned on non-2xx polling responses.
type deviceTokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// PollDeviceToken makes a single poll request to the token endpoint.
// On success it returns the token response. On pending/slow_down it
// returns (nil, nil). On terminal errors it returns an error.
func (c *Client) PollDeviceToken(deviceCode string) (*DeviceTokenResponse, error) {
	reqBody := deviceTokenRequest{
		GrantType:  "urn:ietf:params:oauth:grant-type:device_code",
		DeviceCode: deviceCode,
		ClientID:   c.ClientID,
	}

	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseURL+"/user_management/authenticate", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading poll response: %w", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var tokenResp DeviceTokenResponse
		if err := json.Unmarshal(respBytes, &tokenResp); err != nil {
			return nil, fmt.Errorf("decoding token response: %w", err)
		}
		return &tokenResp, nil
	}

	// Parse the error response.
	var errResp deviceTokenError
	if err := json.Unmarshal(respBytes, &errResp); err != nil {
		return nil, fmt.Errorf("poll returned %d: %s", resp.StatusCode, truncate(respBytes, 512))
	}

	switch errResp.Error {
	case "authorization_pending", "slow_down":
		// Not ready yet – caller should wait and retry.
		return nil, nil
	case "access_denied":
		return nil, fmt.Errorf("user denied authorization")
	case "expired_token":
		return nil, fmt.Errorf("device code expired")
	default:
		return nil, fmt.Errorf("poll error: %s: %s", errResp.Error, errResp.ErrorDescription)
	}
}

// ---------------------------------------------------------------------------
// Organization membership (convenience – not used in init but commonly
// needed alongside the above)
// ---------------------------------------------------------------------------

// OrganizationMembership mirrors the WorkOS object of the same name.
type OrganizationMembership struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id"`
	Role           string `json:"role,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
}

// ListOrganizationMemberships returns org memberships for the user.
//
//	GET /user_management/users/{id}/organization_memberships
func (c *Client) ListOrganizationMemberships(userID string) ([]OrganizationMembership, error) {
	var resp listResponse[OrganizationMembership]
	err := c.doJSON("GET",
		"/user_management/users/"+url.PathEscape(userID)+"/organization_memberships",
		nil, &resp,
	)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}
