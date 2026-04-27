package dolthubauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	checkErr         error
	connectExpiresAt time.Time
	patchCalled      bool
}

func (f fakeStore) Check(context.Context) error { return f.checkErr }
func (f fakeStore) CreateConnectToken(context.Context, []byte, []byte, string, UserMetadata, time.Time, time.Time) error {
	return nil
}

func (f fakeStore) RedeemConnectToken(context.Context, RedeemInput) (*Connection, error) {
	return nil, ErrNotFound
}

func (f fakeStore) GetConnection(context.Context, string, string, string, string) (*Connection, error) {
	return nil, ErrNotFound
}

func (f fakeStore) GetConnectionCredential(context.Context, string, string, string, string, CredentialCipher) (*Connection, string, error) {
	return nil, "", ErrNotFound
}

func (f fakeStore) PatchRigHandle(context.Context, string, string, string, string, string, int, time.Time) (*Connection, error) {
	return nil, ErrNotFound
}

func (f fakeStore) UpsertWasteland(context.Context, string, string, string, string, WastelandConfig, int, time.Time) (*Connection, error) {
	return nil, ErrNotFound
}

func (f fakeStore) DeleteWasteland(context.Context, string, string, string, string, string, int, time.Time) (*Connection, error) {
	return nil, ErrNotFound
}

func (f fakeStore) PatchWastelandSettings(context.Context, string, string, string, string, string, string, bool, int, time.Time) (*Connection, error) {
	return nil, ErrNotFound
}

func (f fakeStore) UseServiceNonce(context.Context, string, string, time.Time, time.Time) error {
	return nil
}

type fakeCipher struct {
	checkErr error
}

func (f fakeCipher) Check(context.Context) error { return f.checkErr }
func (f fakeCipher) Encrypt(context.Context, []byte) ([]byte, string, string, error) {
	return []byte("cipher"), localEncryptionBackend, localEncryptionBackend, nil
}

func (f fakeCipher) Decrypt(context.Context, []byte, string, string) ([]byte, error) {
	return []byte("plain"), nil
}

type proxyStore struct {
	fakeStore
	apiKey string
	conn   *Connection
}

func (p proxyStore) GetConnectionCredential(context.Context, string, string, string, string, CredentialCipher) (*Connection, string, error) {
	if p.conn == nil {
		return nil, "", ErrNotFound
	}
	return p.conn, p.apiKey, nil
}

func TestNewServer_RequiresDependencies(t *testing.T) {
	_, err := NewServer(validConfig(), Dependencies{})
	if err == nil || !strings.Contains(err.Error(), "auth-service store is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestServerHealthEndpoints(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC) }
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      fakeStore{},
		KeyManager: fakeCipher{},
		Now:        now,
		Version:    "test-version",
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	t.Run("livez", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/livez", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("readyz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"status":"ready"`) {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestServerReadinessFailure(t *testing.T) {
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      fakeStore{checkErr: errors.New("db down")},
		KeyManager: fakeCipher{},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `db down`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestServerRedeemInvalidJSON(t *testing.T) {
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      fakeStore{},
		KeyManager: fakeCipher{},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/connect-tokens/redeem", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Wasteland-Auth-Error-Code"); got != "invalid_json" {
		t.Fatalf("X-Wasteland-Auth-Error-Code = %q", got)
	}
}

func TestServerRedeemExpiredTokenReturnsCORSJSON(t *testing.T) {
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      expiredRedeemStore{},
		KeyManager: fakeCipher{},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	reqBody, _ := json.Marshal(RedeemConnectTokenRequest{
		ConnectToken: "connect-token",
		RedeemSecret: "redeem-secret",
		APIKey:       "secret-token",
		Metadata: UserMetadata{
			RigHandle: "alice",
			Wastelands: []WastelandConfig{{
				Upstream: "hop/wl-commons",
				ForkOrg:  "alice-org",
				ForkDB:   "wl-commons",
				Mode:     "pr",
				Signing:  true,
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/connect-tokens/redeem", bytes.NewReader(reqBody))
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("X-Wasteland-Auth-Error-Code"); got != "expired_connect_token" {
		t.Fatalf("X-Wasteland-Auth-Error-Code = %q", got)
	}
	if !strings.Contains(rec.Body.String(), `"error_code":"expired_connect_token"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

type expiredRedeemStore struct{ fakeStore }

func (f expiredRedeemStore) RedeemConnectToken(context.Context, RedeemInput) (*Connection, error) {
	return nil, ErrExpiredConnectToken
}

func TestServerCORSRejectsUnknownOrigin(t *testing.T) {
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      fakeStore{},
		KeyManager: fakeCipher{},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodOptions, "/v1/connect-tokens/redeem", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestLocalMasterKey_RequiresValue(t *testing.T) {
	if _, err := NewLocalMasterKey(" "); err == nil {
		t.Fatal("expected error for empty master key")
	}
}

type captureStore struct {
	fakeStore
}

func (f *captureStore) Check(context.Context) error { return f.checkErr }
func (f *captureStore) CreateConnectToken(_ context.Context, _ []byte, _ []byte, _ string, _ UserMetadata, expiresAt, _ time.Time) error {
	f.connectExpiresAt = expiresAt
	return nil
}

func (f *captureStore) PatchRigHandle(context.Context, string, string, string, string, string, int, time.Time) (*Connection, error) {
	f.patchCalled = true
	return nil, ErrNotFound
}

func TestCopyProxyHeaders_StripsInternalServiceAuthHeaders(t *testing.T) {
	src := http.Header{
		"Authorization":        []string{"Bearer top-secret"},
		headerServiceTimestamp: []string{"2026-04-08T12:00:00Z"},
		headerServiceNonce:     []string{"nonce-123"},
		headerServiceBodySHA:   []string{"body-sha"},
		headerAuthTenantID:     []string{"tenant-dev"},
		headerAuthEnvironment:  []string{"dev"},
		headerAuthSubjectID:    []string{"subject-1"},
		headerAuthConnectionID: []string{"conn-1"},
		"Content-Type":         []string{"application/json"},
		"X-Custom-Forwarded":   []string{"allowed"},
	}
	dst := http.Header{}

	copyProxyHeaders(dst, src)

	if got := dst.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := dst.Get("X-Custom-Forwarded"); got != "allowed" {
		t.Fatalf("X-Custom-Forwarded = %q", got)
	}
	for _, key := range []string{
		"Authorization",
		headerServiceTimestamp,
		headerServiceNonce,
		headerServiceBodySHA,
		headerAuthTenantID,
		headerAuthEnvironment,
		headerAuthSubjectID,
		headerAuthConnectionID,
	} {
		if got := dst.Get(key); got != "" {
			t.Fatalf("%s leaked as %q", key, got)
		}
	}
}

func TestServerCreateConnectToken_ClampsTTL(t *testing.T) {
	store := &captureStore{}
	now := time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      store,
		KeyManager: fakeCipher{},
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	body := []byte(`{"subject_id":"subject-1","metadata":{"rig_handle":"alice","wastelands":[{"upstream":"hop/wl-commons","fork_org":"alice-org","fork_db":"wl-commons","mode":"pr","signing":true}]},"ttl_seconds":3600}`)
	req := signedServiceRequest(t, http.MethodPost, "/v1/connect-tokens", body, "subject-1", "")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := store.connectExpiresAt; !got.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("connect token expires at %s, want %s", got, now.Add(5*time.Minute))
	}
}

func TestServerPatchRigHandle_RejectsInvalidMetadata(t *testing.T) {
	store := &captureStore{}
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      store,
		KeyManager: fakeCipher{},
		Now:        func() time.Time { return time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	body := []byte(`{"record_version":3,"rig_handle":"bad handle"}`)
	req := signedServiceRequest(t, http.MethodPatch, "/v1/connections/conn-1/rig-handle", body, "subject-1", "conn-1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Wasteland-Auth-Error-Code"); got != "invalid_metadata" {
		t.Fatalf("X-Wasteland-Auth-Error-Code = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "rig_handle") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if store.patchCalled {
		t.Fatal("store patch should not have been called for invalid rig_handle")
	}
}

func signedServiceRequest(t *testing.T, method, path string, body []byte, subjectID, connectionID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	nonce := "nonce-123"
	now := time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)
	timestamp, signature := signServiceRequest(
		"current-secret",
		"current-key",
		now,
		nonce,
		method,
		serviceAuthRequestTarget(req.URL),
		body,
		"tenant-dev",
		"dev",
		subjectID,
		connectionID,
	)
	req.Header.Set(headerAuthorization, serviceAuthPrefix+"current-key:"+signature)
	req.Header.Set(headerServiceTimestamp, timestamp)
	req.Header.Set(headerServiceNonce, nonce)
	req.Header.Set(headerServiceBodySHA, bodySHA256(body))
	req.Header.Set(headerAuthTenantID, "tenant-dev")
	req.Header.Set(headerAuthEnvironment, "dev")
	req.Header.Set(headerAuthSubjectID, subjectID)
	if connectionID != "" {
		req.Header.Set(headerAuthConnectionID, connectionID)
	}
	return req
}

func TestServerRedeemValidationError_MarksRetryableOutage(t *testing.T) {
	redeemStore := fakeStore{}
	srv, err := NewServer(validConfig(), Dependencies{
		Store:      redeemStore,
		KeyManager: fakeCipher{},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	srv.deps.Store = fakeRedeemValidationStore{}

	reqBody, _ := json.Marshal(RedeemConnectTokenRequest{
		ConnectToken: "connect-token",
		RedeemSecret: "redeem-secret",
		APIKey:       "secret-token",
		Metadata: UserMetadata{
			RigHandle: "alice",
			Wastelands: []WastelandConfig{{
				Upstream: "hop/wl-commons",
				ForkOrg:  "alice-org",
				ForkDB:   "wl-commons",
				Mode:     "pr",
				Signing:  true,
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/connect-tokens/redeem", bytes.NewReader(reqBody))
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Wasteland-Auth-Error-Code"); got != string(ValidationUpstreamUnreachable) {
		t.Fatalf("X-Wasteland-Auth-Error-Code = %q", got)
	}
}

type fakeRedeemValidationStore struct{ fakeStore }

func (f fakeRedeemValidationStore) RedeemConnectToken(context.Context, RedeemInput) (*Connection, error) {
	return nil, &ValidationError{Code: ValidationUpstreamUnreachable, Err: errors.New("probe timeout")}
}

func TestServerProxyStripsInternalServiceHeadersBeforeForwarding(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	var gotReq *http.Request

	cfg := validConfig()
	srv, err := NewServer(cfg, Dependencies{
		Store: proxyStore{
			apiKey: "stored-api-key",
			conn: &Connection{
				ConnectionID: "conn-1",
				TenantID:     cfg.TenantID,
				Environment:  cfg.Environment,
				SubjectID:    "subject-1",
				Metadata: UserMetadata{
					RigHandle: "alice",
					Wastelands: []WastelandConfig{{
						Upstream: "hop/wl-commons",
						ForkOrg:  "alice",
						ForkDB:   "wl-commons",
						Mode:     "pr",
						Signing:  true,
					}},
				},
				Status:        StatusActive,
				RecordVersion: 1,
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		},
		KeyManager: fakeCipher{},
		Now:        func() time.Time { return now },
		ProxyHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotReq = req.Clone(req.Context())
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				}, nil
			}),
		},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/proxy/api/hop/wl-commons/main?q=SELECT%201", nil)
	nonce := "nonce-1"
	timestamp, signature := signServiceRequest(
		cfg.CurrentSharedSecret,
		cfg.CurrentKeyID,
		now,
		nonce,
		http.MethodGet,
		serviceAuthRequestTarget(req.URL),
		nil,
		cfg.TenantID,
		cfg.Environment,
		"subject-1",
		"conn-1",
	)
	req.Header.Set(headerAuthorization, serviceAuthPrefix+cfg.CurrentKeyID+":"+signature)
	req.Header.Set(headerServiceTimestamp, timestamp)
	req.Header.Set(headerServiceNonce, nonce)
	req.Header.Set(headerServiceBodySHA, bodySHA256(nil))
	req.Header.Set(headerAuthTenantID, cfg.TenantID)
	req.Header.Set(headerAuthEnvironment, cfg.Environment)
	req.Header.Set(headerAuthSubjectID, "subject-1")
	req.Header.Set(headerAuthConnectionID, "conn-1")
	req.Header.Set("X-Request-Id", "req-123")
	req.Header.Set("X-Custom", "keep-me")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if gotReq == nil {
		t.Fatal("expected upstream proxy request")
	}
	if got := gotReq.Header.Get("Authorization"); got != "token stored-api-key" {
		t.Fatalf("Authorization = %q", got)
	}
	for _, name := range []string{
		headerServiceTimestamp,
		headerServiceNonce,
		headerServiceBodySHA,
		headerAuthTenantID,
		headerAuthEnvironment,
		headerAuthSubjectID,
		headerAuthConnectionID,
	} {
		if got := gotReq.Header.Get(name); got != "" {
			t.Fatalf("%s leaked upstream as %q", name, got)
		}
	}
	if got := gotReq.Header.Get("X-Custom"); got != "keep-me" {
		t.Fatalf("X-Custom = %q", got)
	}
	if got := gotReq.Header.Get("X-Request-Id"); got != "req-123" {
		t.Fatalf("X-Request-Id = %q", got)
	}
}

func TestServerProxyPreservesEscapedBranchNames(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	var gotReq *http.Request

	cfg := validConfig()
	srv, err := NewServer(cfg, Dependencies{
		Store: proxyStore{
			apiKey: "stored-api-key",
			conn: &Connection{
				ConnectionID: "conn-1",
				TenantID:     cfg.TenantID,
				Environment:  cfg.Environment,
				SubjectID:    "subject-1",
				Metadata:     UserMetadata{RigHandle: "alice"},
				Status:       StatusActive,
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		},
		KeyManager: fakeCipher{},
		Now:        func() time.Time { return now },
		ProxyHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotReq = req.Clone(req.Context())
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				}, nil
			}),
		},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/proxy/api/alice/wl-commons/write/main/wl%2Fregister%2Falice?q=SELECT%201",
		nil,
	)
	nonce := "nonce-1"
	timestamp, signature := signServiceRequest(
		cfg.CurrentSharedSecret,
		cfg.CurrentKeyID,
		now,
		nonce,
		http.MethodPost,
		serviceAuthRequestTarget(req.URL),
		nil,
		cfg.TenantID,
		cfg.Environment,
		"subject-1",
		"conn-1",
	)
	req.Header.Set(headerAuthorization, serviceAuthPrefix+cfg.CurrentKeyID+":"+signature)
	req.Header.Set(headerServiceTimestamp, timestamp)
	req.Header.Set(headerServiceNonce, nonce)
	req.Header.Set(headerServiceBodySHA, bodySHA256(nil))
	req.Header.Set(headerAuthTenantID, cfg.TenantID)
	req.Header.Set(headerAuthEnvironment, cfg.Environment)
	req.Header.Set(headerAuthSubjectID, "subject-1")
	req.Header.Set(headerAuthConnectionID, "conn-1")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if gotReq == nil {
		t.Fatal("expected upstream proxy request")
	}
	if got := gotReq.URL.EscapedPath(); got != "/api/v1alpha1/alice/wl-commons/write/main/wl%2Fregister%2Falice" {
		t.Fatalf("EscapedPath = %q", got)
	}
}
