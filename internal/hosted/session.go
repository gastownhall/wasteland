// Package hosted provides multi-tenant hosted mode with Nango credential delegation.
package hosted

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// UserSession represents an authenticated browser session.
type UserSession struct {
	ID           string
	ConnectionID string // Nango connection ID (set after DoltHub connect)
	CreatedAt    time.Time
}

// SessionStore is a thread-safe in-memory session store.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*UserSession
}

// NewSessionStore creates a new empty SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[string]*UserSession)}
}

// Create creates a new session with the given Nango connection ID.
func (s *SessionStore) Create(connectionID string) string {
	id := generateSessionID()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = &UserSession{
		ID:           id,
		ConnectionID: connectionID,
		CreatedAt:    time.Now(),
	}
	return id
}

// Get retrieves a session by ID.
func (s *SessionStore) Get(id string) (*UserSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// Delete removes a session by ID.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Restore re-creates a session from cookie data after a server restart.
func (s *SessionStore) Restore(sessionID, connectionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = &UserSession{
		ID:           sessionID,
		ConnectionID: connectionID,
		CreatedAt:    time.Now(),
	}
}

func generateSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

const cookieName = "wl_session"

// SignSessionID signs a session ID with the given secret using HMAC-SHA256.
func SignSessionID(id, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	sig := hex.EncodeToString(mac.Sum(nil))
	return id + "." + sig
}

// VerifySessionID verifies a signed session cookie value. Returns the session ID if valid.
func VerifySessionID(signed, secret string) (string, bool) {
	// Find the last dot separator.
	dot := -1
	for i := len(signed) - 1; i >= 0; i-- {
		if signed[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 || dot == 0 || dot == len(signed)-1 {
		return "", false
	}
	id := signed[:dot]
	sig := signed[dot+1:]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", false
	}
	return id, true
}

// SignSessionCookie signs a session cookie containing both sessionID and connectionID.
// Format: sessionID.connectionID.HMAC(sessionID.connectionID, secret)
func SignSessionCookie(sessionID, connectionID, secret string) string {
	payload := sessionID + "." + connectionID
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

// VerifySessionCookie verifies a signed session cookie in the new 3-segment format.
// Returns (sessionID, connectionID, ok).
func VerifySessionCookie(signed, secret string) (string, string, bool) {
	// Split into exactly 3 segments: sessionID, connectionID, signature.
	parts := strings.SplitN(signed, ".", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	sessionID, connectionID, sig := parts[0], parts[1], parts[2]

	payload := sessionID + "." + connectionID
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", "", false
	}
	return sessionID, connectionID, true
}

// SetSessionCookie sets the wl_session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, sessionID, connectionID, secret string) {
	signed := SignSessionCookie(sessionID, connectionID, secret)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie clears the wl_session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// ReadSessionCookie reads and verifies the session cookie from the request.
// Supports both old format (sessionID.sig → empty connectionID) and new format
// (sessionID.connectionID.sig).
func ReadSessionCookie(r *http.Request, secret string) (string, string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", "", false
	}

	// Try new 3-segment format first.
	if sessionID, connectionID, ok := VerifySessionCookie(c.Value, secret); ok {
		return sessionID, connectionID, true
	}

	// Fall back to old 2-segment format (connectionID will be empty).
	if sessionID, ok := VerifySessionID(c.Value, secret); ok {
		return sessionID, "", true
	}

	return "", "", false
}
