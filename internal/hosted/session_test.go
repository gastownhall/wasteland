package hosted

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionStore_CreateAndGet(t *testing.T) {
	store := NewSessionStore()
	id := store.Create("conn-123")
	if id == "" {
		t.Fatal("expected non-empty session ID")
	}

	sess, ok := store.Get(id)
	if !ok {
		t.Fatal("expected session to exist")
	}
	if sess.ConnectionID != "conn-123" {
		t.Errorf("expected conn-123, got %s", sess.ConnectionID)
	}
	if sess.ID != id {
		t.Errorf("expected ID %s, got %s", id, sess.ID)
	}
}

func TestSessionStore_GetMissing(t *testing.T) {
	store := NewSessionStore()
	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("expected session not to exist")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewSessionStore()
	id := store.Create("conn-123")
	store.Delete(id)

	_, ok := store.Get(id)
	if ok {
		t.Error("expected session to be deleted")
	}
}

func TestSessionStore_UniqueIDs(t *testing.T) {
	store := NewSessionStore()
	id1 := store.Create("conn-1")
	id2 := store.Create("conn-2")
	if id1 == id2 {
		t.Error("expected unique session IDs")
	}
}

func TestSignVerifySessionID(t *testing.T) {
	secret := "test-secret"
	id := "abc123"

	signed := SignSessionID(id, secret)
	if signed == id {
		t.Error("signed value should differ from plain ID")
	}

	got, ok := VerifySessionID(signed, secret)
	if !ok {
		t.Fatal("expected verification to succeed")
	}
	if got != id {
		t.Errorf("expected %s, got %s", id, got)
	}
}

func TestVerifySessionID_WrongSecret(t *testing.T) {
	signed := SignSessionID("abc123", "secret-1")
	_, ok := VerifySessionID(signed, "secret-2")
	if ok {
		t.Error("expected verification to fail with wrong secret")
	}
}

func TestVerifySessionID_Tampered(t *testing.T) {
	signed := SignSessionID("abc123", "secret")
	tampered := "tampered." + signed[len("abc123."):]
	_, ok := VerifySessionID(tampered, "secret")
	if ok {
		t.Error("expected verification to fail for tampered value")
	}
}

func TestVerifySessionID_Invalid(t *testing.T) {
	for _, val := range []string{"", "noseparator", ".leading", "trailing."} {
		_, ok := VerifySessionID(val, "secret")
		if ok {
			t.Errorf("expected verification to fail for %q", val)
		}
	}
}

func TestSetAndReadSessionCookie(t *testing.T) {
	secret := "cookie-secret"
	sessionID := "sess-42"
	connectionID := "conn-99"

	w := httptest.NewRecorder()
	SetSessionCookie(w, sessionID, connectionID, secret)

	// Extract the cookie from the response and put it in a request.
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "wl_session" {
		t.Errorf("expected cookie name wl_session, got %s", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}
	if !cookie.Secure {
		t.Error("expected Secure cookie")
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)

	gotID, gotConn, ok := ReadSessionCookie(req, secret)
	if !ok {
		t.Fatal("expected cookie to be valid")
	}
	if gotID != sessionID {
		t.Errorf("expected session %s, got %s", sessionID, gotID)
	}
	if gotConn != connectionID {
		t.Errorf("expected connection %s, got %s", connectionID, gotConn)
	}
}

func TestReadSessionCookie_Missing(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	_, _, ok := ReadSessionCookie(req, "secret")
	if ok {
		t.Error("expected no cookie")
	}
}

func TestClearSessionCookie(t *testing.T) {
	w := httptest.NewRecorder()
	ClearSessionCookie(w)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("expected MaxAge -1, got %d", cookies[0].MaxAge)
	}
}

func TestClearSessionCookie_OverwritesExisting(t *testing.T) {
	secret := "secret"
	w := httptest.NewRecorder()
	SetSessionCookie(w, "sess-1", "conn-1", secret)

	// Now clear it.
	w2 := httptest.NewRecorder()
	ClearSessionCookie(w2)

	// Build a request with just the cleared cookie.
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range w2.Result().Cookies() {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}

	_, _, ok := ReadSessionCookie(req, secret)
	if ok {
		t.Error("expected cleared cookie to fail verification")
	}
}

func TestSignVerifySessionCookie(t *testing.T) {
	secret := "test-secret"
	sessionID := "sess-1"
	connectionID := "conn-1"

	signed := SignSessionCookie(sessionID, connectionID, secret)
	gotSess, gotConn, ok := VerifySessionCookie(signed, secret)
	if !ok {
		t.Fatal("expected verification to succeed")
	}
	if gotSess != sessionID {
		t.Errorf("expected session %s, got %s", sessionID, gotSess)
	}
	if gotConn != connectionID {
		t.Errorf("expected connection %s, got %s", connectionID, gotConn)
	}
}

func TestVerifySessionCookie_WrongSecret(t *testing.T) {
	signed := SignSessionCookie("sess-1", "conn-1", "secret-1")
	_, _, ok := VerifySessionCookie(signed, "secret-2")
	if ok {
		t.Error("expected verification to fail with wrong secret")
	}
}

func TestVerifySessionCookie_Tampered(t *testing.T) {
	signed := SignSessionCookie("sess-1", "conn-1", "secret")
	// Tamper with the connectionID segment.
	tampered := "sess-1.tampered." + signed[len("sess-1.conn-1."):]
	_, _, ok := VerifySessionCookie(tampered, "secret")
	if ok {
		t.Error("expected verification to fail for tampered value")
	}
}

func TestVerifySessionCookie_Invalid(t *testing.T) {
	for _, val := range []string{"", "one-segment", "two.segments", ".empty.first", "a..c", "a.b."} {
		_, _, ok := VerifySessionCookie(val, "secret")
		if ok {
			t.Errorf("expected verification to fail for %q", val)
		}
	}
}

func TestReadSessionCookie_OldFormat(t *testing.T) {
	// Old-format cookie (sessionID.sig) should still be readable with empty connectionID.
	secret := "cookie-secret"
	sessionID := "old-sess"
	signed := SignSessionID(sessionID, secret)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: signed})

	gotID, gotConn, ok := ReadSessionCookie(req, secret)
	if !ok {
		t.Fatal("expected old-format cookie to be valid")
	}
	if gotID != sessionID {
		t.Errorf("expected session %s, got %s", sessionID, gotID)
	}
	if gotConn != "" {
		t.Errorf("expected empty connectionID, got %s", gotConn)
	}
}

func TestSessionStore_Restore(t *testing.T) {
	store := NewSessionStore()

	// Session should not exist yet.
	_, ok := store.Get("sess-1")
	if ok {
		t.Fatal("expected session to not exist before restore")
	}

	store.Restore("sess-1", "conn-1")

	sess, ok := store.Get("sess-1")
	if !ok {
		t.Fatal("expected session to exist after restore")
	}
	if sess.ID != "sess-1" {
		t.Errorf("expected ID sess-1, got %s", sess.ID)
	}
	if sess.ConnectionID != "conn-1" {
		t.Errorf("expected conn-1, got %s", sess.ConnectionID)
	}
}
