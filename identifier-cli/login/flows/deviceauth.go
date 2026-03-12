package flows

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const workosBaseURL = "https://api.workos.com"

// DeviceAuthClient is a minimal HTTP client for the WorkOS device
// authorization flow. It only needs a client ID (no API key).
type DeviceAuthClient struct {
	ClientID   string
	HTTPClient *http.Client
}

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

// DeviceTokenUser is a minimal user object returned by the token endpoint.
type DeviceTokenUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// DeviceTokenResponse holds the successful authentication result from
// polling the token endpoint.
type DeviceTokenResponse struct {
	User         *DeviceTokenUser `json:"user"`
	AccessToken  string           `json:"access_token"`
	RefreshToken string           `json:"refresh_token"`
}

// RequestDeviceAuthorization initiates the device authorization flow.
func (c *DeviceAuthClient) RequestDeviceAuthorization() (*DeviceAuthorization, error) {
	body, _ := json.Marshal(map[string]string{"client_id": c.ClientID})

	resp, err := c.do("POST", "/user_management/authorize/device", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("device auth returned %d: %s", resp.StatusCode, truncateBytes(respBytes, 512))
	}

	var da DeviceAuthorization
	if err := json.Unmarshal(respBytes, &da); err != nil {
		return nil, fmt.Errorf("decoding device auth response: %w", err)
	}
	return &da, nil
}

// PollDeviceToken makes a single poll request to the token endpoint.
// On success it returns the token response. On pending/slow_down it
// returns (nil, nil). On terminal errors it returns an error.
func (c *DeviceAuthClient) PollDeviceToken(deviceCode string) (*DeviceTokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
		"client_id":   c.ClientID,
	})

	resp, err := c.do("POST", "/user_management/authenticate", body)
	if err != nil {
		return nil, err
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

	var errResp struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description,omitempty"`
	}
	if err := json.Unmarshal(respBytes, &errResp); err != nil {
		return nil, fmt.Errorf("poll returned %d: %s", resp.StatusCode, truncateBytes(respBytes, 512))
	}

	switch errResp.Error {
	case "authorization_pending", "slow_down":
		return nil, nil
	case "access_denied":
		return nil, fmt.Errorf("user denied authorization")
	case "expired_token":
		return nil, fmt.Errorf("device code expired")
	default:
		return nil, fmt.Errorf("poll error: %s: %s", errResp.Error, errResp.ErrorDescription)
	}
}

func (c *DeviceAuthClient) do(method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(method, workosBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req)
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
