// github.com/accretional/runrpc/identifier/workos/session.go
package workos

import (
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Domain types – these mirror the WorkOS API shapes we care about, kept
// deliberately lightweight so the package doesn't depend on a generated
// client for these read-only structs.
// ---------------------------------------------------------------------------

// User is a minimal representation of a WorkOS user object.
type User struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	FirstName     string `json:"first_name,omitempty"`
	LastName      string `json:"last_name,omitempty"`
	EmailVerified bool   `json:"email_verified"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// Identity represents a single federated identity linked to a user.
type Identity struct {
	IdP          string `json:"idp"`
	IdPID        string `json:"idp_id"`
	Type         string `json:"type"`
	ProviderName string `json:"provider_name,omitempty"`
}

// Session is a minimal representation of a WorkOS user session.
type Session struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	IPAddress string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// Invitation represents a pending WorkOS invitation.
type Invitation struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	State     string `json:"state,omitempty"`
	Token     string `json:"token,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// ---------------------------------------------------------------------------
// UserState aggregates everything we know about a user at runtime.
// ---------------------------------------------------------------------------

// UserState holds the fetched/computed state for a single user.  Fields
// are nil/zero when not yet populated.
type UserState struct {
	User       *User
	Identities []Identity
	Sessions   []Session

	// AccessToken is the OAuth access token obtained during authentication.
	AccessToken string

	// RefreshToken is the OAuth refresh token, if provided.
	RefreshToken string

	// PendingInvitationID is set when the user was added through the
	// invitation flow and hasn't authenticated yet.
	PendingInvitationID string

	// LastRefresh records when this entry was last updated from the API.
	LastRefresh time.Time
}

// ---------------------------------------------------------------------------
// Package-level user map
//
// Keyed by user ID (from the access-token flow) or by email (from the
// invitation flow, non-default only).  A sync.Map keeps lock contention
// minimal for the expected read-heavy access pattern.
// ---------------------------------------------------------------------------

var users sync.Map // map[string]*UserState

// StoreUser upserts a UserState into the package-level map.
func StoreUser(key string, state *UserState) {
	state.LastRefresh = time.Now()
	users.Store(key, state)
}

// LoadUser retrieves a UserState.  Returns nil, false when the key is
// absent.
func LoadUser(key string) (*UserState, bool) {
	v, ok := users.Load(key)
	if !ok {
		return nil, false
	}
	return v.(*UserState), true
}

// DeleteUser removes a user from the map (e.g. on session revocation).
func DeleteUser(key string) {
	users.Delete(key)
}

// RangeUsers iterates over every user in the map.  Return false from fn
// to stop early.
func RangeUsers(fn func(key string, state *UserState) bool) {
	users.Range(func(k, v interface{}) bool {
		return fn(k.(string), v.(*UserState))
	})
}

// UserCount returns the current number of tracked users.  O(n) but the
// map is expected to be small.
func UserCount() int {
	n := 0
	users.Range(func(_, _ interface{}) bool {
		n++
		return true
	})
	return n
}

// UpdateUserSessions is a convenience helper that fetches fresh sessions
// from the API and patches the stored state in-place.
func UpdateUserSessions(c *Client, userID string) error {
	sessions, err := c.ListSessions(userID)
	if err != nil {
		return err
	}

	v, ok := users.Load(userID)
	if !ok {
		// User not tracked yet – create a minimal entry.
		StoreUser(userID, &UserState{Sessions: sessions})
		return nil
	}

	state := v.(*UserState)
	state.Sessions = sessions
	state.LastRefresh = time.Now()
	users.Store(userID, state)
	return nil
}

// UpdateUserIdentities is the identity-list equivalent of
// UpdateUserSessions.
func UpdateUserIdentities(c *Client, userID string) error {
	identities, err := c.ListIdentities(userID)
	if err != nil {
		return err
	}

	v, ok := users.Load(userID)
	if !ok {
		StoreUser(userID, &UserState{Identities: identities})
		return nil
	}

	state := v.(*UserState)
	state.Identities = identities
	state.LastRefresh = time.Now()
	users.Store(userID, state)
	return nil
}
