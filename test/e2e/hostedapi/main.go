// Command hostedapi runs an in-process hosted mode backend against the
// DoltHub double for multi-actor e2e scenarios that need the full hosted
// auth + federation stack without any real upstream dependency.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/wasteland/internal/api"
	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/dolthubauth"
	"github.com/gastownhall/wasteland/internal/hosted"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/gastownhall/wasteland/test/e2e/dolthubdouble"
)

const (
	defaultAddr      = "127.0.0.1:8999"
	upstreamOrg      = "e2e"
	upstreamDB       = "wl-commons"
	upstreamName     = upstreamOrg + "/" + upstreamDB
	sessionSecret    = "session-secret"
	subjectSecret    = "subject-secret"
	authKeyID        = "e2e-kid"
	authShared       = "e2e-shared-secret"
	authTenantID     = "tenant-1"
	authEnvironment  = "staging"
	serviceAuthPrefx = "Wasteland-HMAC "
)

type hostedRequestLog struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type testState struct {
	HostedRequests []hostedRequestLog     `json:"hosted_requests"`
	DoltHub        dolthubdouble.Snapshot `json:"dolthub"`
}

type testSessionResponse struct {
	SessionCookieName  string `json:"session_cookie_name"`
	SessionCookieValue string `json:"session_cookie_value"`
	SubjectCookieName  string `json:"subject_cookie_name"`
	SubjectCookieValue string `json:"subject_cookie_value"`
}

type testSessionRequest struct {
	Actor string `json:"actor"`
}

type mergePRRequest struct {
	PRID string `json:"pr_id"`
}

type actorConfig struct {
	Handle       string
	DisplayName  string
	SubjectID    string
	ConnectionID string
}

type serviceScope struct {
	SubjectID    string
	ConnectionID string
}

type serverState struct {
	baseURL  string
	double   *dolthubdouble.Server
	sessions *hosted.SessionStore
	resolver *hosted.AuthServiceWorkspaceResolver
	actors   map[string]actorConfig

	mu             sync.Mutex
	hostedRequests []hostedRequestLog
}

var defaultActors = []actorConfig{
	{
		Handle:       "alice",
		DisplayName:  "Alice",
		SubjectID:    "subject-alice",
		ConnectionID: "conn-alice",
	},
	{
		Handle:       "bob",
		DisplayName:  "Bob",
		SubjectID:    "subject-bob",
		ConnectionID: "conn-bob",
	},
	{
		Handle:       "charlie",
		DisplayName:  "Charlie",
		SubjectID:    "subject-charlie",
		ConnectionID: "conn-charlie",
	},
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := flag.String("addr", defaultAddr, "listen address")
	root := flag.String("root", "", "working directory for the fake DoltHub state")
	flag.Parse()

	double, err := dolthubdouble.New(*root)
	if err != nil {
		return fmt.Errorf("create DoltHub double: %w", err)
	}
	defer func() { _ = double.Close() }()

	baseURL := "http://" + *addr

	state := &serverState{
		baseURL: baseURL,
		double:  double,
		actors:  make(map[string]actorConfig, len(defaultActors)),
	}
	for _, actor := range defaultActors {
		state.actors[actor.Handle] = actor
	}
	if err := state.reset(); err != nil {
		return fmt.Errorf("reset e2e state: %w", err)
	}

	authClient := dolthubauth.NewClient(dolthubauth.ClientConfig{
		BaseURL:      baseURL + "/__auth",
		TenantID:     authTenantID,
		Environment:  authEnvironment,
		KeyID:        authKeyID,
		SharedSecret: authShared,
		Now:          time.Now,
	})

	sessions := hosted.NewSessionStore()
	state.sessions = sessions
	resolver := hosted.NewAuthServiceWorkspaceResolver(authClient, sessions)
	defer resolver.Stop()
	state.resolver = resolver

	hostedServer := hosted.NewAuthServiceServer(resolver, sessions, authClient, sessionSecret, subjectSecret, authEnvironment)
	apiServer := api.NewHostedWorkspace(hosted.NewClientFunc(), hosted.NewWorkspaceFunc())
	apiServer.SetEnvironment(authEnvironment)

	publicDB := backend.NewRemoteDBWithClient(newFakeDoltHubClient(baseURL), upstreamOrg, upstreamDB, upstreamOrg, upstreamDB, "")
	scoreboardCache := api.NewScoreboardCache(publicDB, 200*time.Millisecond)
	apiServer.SetScoreboard(scoreboardCache)

	detailCache := api.NewCachedEndpoint(func() ([]byte, error) {
		entries, err := commons.QueryScoreboardDetail(publicDB, 100)
		if err != nil {
			return nil, err
		}
		return json.Marshal(api.ToScoreboardDetailResponse(entries))
	}, 200*time.Millisecond)
	apiServer.SetScoreboardDetail(detailCache)

	dumpCache := api.NewCachedEndpoint(func() ([]byte, error) {
		dump, err := commons.QueryScoreboardDump(publicDB)
		if err != nil {
			return nil, err
		}
		return json.Marshal(api.ToScoreboardDumpResponse(dump))
	}, 200*time.Millisecond)
	apiServer.SetScoreboardDump(dumpCache)

	apiServer.SetPublicClient(sdk.New(sdk.ClientConfig{
		DB:       publicDB,
		Upstream: upstreamName,
		Mode:     "pr",
	}))

	hostedHandler := state.captureHosted(hostedServer.Handler(apiServer, emptyFS{}))

	mux := http.NewServeMux()
	mux.Handle("/__dolthub/", state.double.Handler())
	mux.HandleFunc("/__auth/v1/connections/", state.handleAuthConnection)
	mux.HandleFunc("/__auth/v1/proxy/api/", state.handleAuthProxyAPI)
	mux.HandleFunc("/__auth/v1/proxy/graphql", state.handleAuthProxyGraphQL)
	mux.HandleFunc("/__test/reset", state.handleReset)
	mux.HandleFunc("/__test/seed", state.handleSeed)
	mux.HandleFunc("/__test/state", state.handleState)
	mux.HandleFunc("/__test/session", state.handleSession)
	mux.HandleFunc("/__test/merge-pr", state.handleMergePR)
	mux.Handle("/", hostedHandler)

	log.Printf("e2e hosted api listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve e2e hosted api: %w", err)
	}
	return nil
}

func (s *serverState) captureHosted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			headers := make(map[string]string)
			for key, values := range r.Header {
				headers[strings.ToLower(key)] = strings.Join(values, ", ")
			}
			var body string
			if r.Body != nil {
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				r.Body = io.NopCloser(strings.NewReader(body))
			}
			s.mu.Lock()
			s.hostedRequests = append(s.hostedRequests, hostedRequestLog{
				Method:  r.Method,
				Path:    r.URL.Path,
				Query:   r.URL.RawQuery,
				Headers: headers,
				Body:    body,
			})
			s.mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

func (s *serverState) reset() error {
	s.mu.Lock()
	s.hostedRequests = nil
	s.mu.Unlock()
	if err := s.double.Reset(); err != nil {
		return err
	}
	return s.double.Seed(dolthubdouble.SeedRequest{
		Repositories: []dolthubdouble.RepositorySeed{
			{
				Owner: upstreamOrg,
				DB:    upstreamDB,
				MainSQL: []string{
					rigInsert("alice", "Alice"),
					rigInsert("bob", "Bob"),
					rigInsert("charlie", "Charlie"),
				},
			},
			{
				Owner:  "alice",
				DB:     upstreamDB,
				ForkOf: &dolthubdouble.RepoRef{Owner: upstreamOrg, DB: upstreamDB},
			},
			{
				Owner:  "bob",
				DB:     upstreamDB,
				ForkOf: &dolthubdouble.RepoRef{Owner: upstreamOrg, DB: upstreamDB},
			},
			{
				Owner:  "charlie",
				DB:     upstreamDB,
				ForkOf: &dolthubdouble.RepoRef{Owner: upstreamOrg, DB: upstreamDB},
			},
		},
	})
}

func (s *serverState) handleAuthConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	scope, ok := s.verifyServiceRequest(w, r)
	if !ok {
		return
	}
	connectionID := strings.TrimPrefix(r.URL.Path, "/__auth/v1/connections/")
	actor, ok := s.actorByConnectionID(connectionID)
	if !ok || scope.SubjectID != actor.SubjectID || scope.ConnectionID != actor.ConnectionID {
		http.Error(w, "connection not found", http.StatusUnauthorized)
		return
	}
	writeJSON(w, s.connectionResponse(actor))
}

func (s *serverState) handleAuthProxyAPI(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.verifyServiceRequest(w, r)
	if !ok {
		return
	}
	actor, found := s.actorByConnectionID(scope.ConnectionID)
	if !found || actor.SubjectID != scope.SubjectID {
		http.Error(w, "connection not found", http.StatusUnauthorized)
		return
	}

	trimmed := strings.TrimPrefix(r.URL.EscapedPath(), "/__auth/v1/proxy/api")
	targetURL := s.baseURL + "/__dolthub/api/v1alpha1" + trimmed
	if rawQuery := r.URL.RawQuery; rawQuery != "" {
		targetURL += "?" + rawQuery
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read proxy body", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "build proxy request", http.StatusInternalServerError)
		return
	}
	copyForwardHeaders(req.Header, r.Header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "proxy request failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	copyResponse(w, resp)
}

func (s *serverState) handleAuthProxyGraphQL(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.verifyServiceRequest(w, r)
	if !ok {
		return
	}
	if _, found := s.actorByConnectionID(scope.ConnectionID); !found {
		http.Error(w, "connection not found", http.StatusUnauthorized)
		return
	}
	http.Error(w, "graphql not implemented in hosted e2e harness", http.StatusNotImplemented)
}

func (s *serverState) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if s.resolver != nil {
		s.resolver.ResetCaches()
	}
	if err := s.reset(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "reset"})
}

func (s *serverState) handleSeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req dolthubdouble.SeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.double.Seed(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "seeded"})
}

func (s *serverState) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	hostedRequests := append([]hostedRequestLog(nil), s.hostedRequests...)
	s.mu.Unlock()
	snapshot, err := s.double.Snapshot(s.baseURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, testState{
		HostedRequests: hostedRequests,
		DoltHub:        snapshot,
	})
}

func (s *serverState) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req testSessionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	actorName := strings.TrimSpace(req.Actor)
	if actorName == "" {
		actorName = "alice"
	}
	actor, ok := s.actors[actorName]
	if !ok {
		http.Error(w, "unknown actor", http.StatusBadRequest)
		return
	}
	sessionID, err := s.sessions.CreateWithSubject(actor.ConnectionID, actor.SubjectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, testSessionResponse{
		SessionCookieName:  "wl_session",
		SessionCookieValue: hosted.SignSessionCookie(sessionID, actor.ConnectionID, sessionSecret),
		SubjectCookieName:  "wl_subject",
		SubjectCookieValue: hosted.SignSubjectID(actor.SubjectID, subjectSecret),
	})
}

func (s *serverState) handleMergePR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req mergePRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.PRID) == "" {
		http.Error(w, "pr_id is required", http.StatusBadRequest)
		return
	}
	if err := s.double.MergePR(req.PRID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "merged"})
}

func rigInsert(handle, displayName string) string {
	return "INSERT INTO rigs (handle, display_name, dolthub_org, registered_at, last_seen) VALUES (" +
		quote(handle) + ", " + quote(displayName) + ", " + quote(handle) + ", NOW(), NOW())"
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func (s *serverState) actorByConnectionID(connectionID string) (actorConfig, bool) {
	for _, actor := range s.actors {
		if actor.ConnectionID == connectionID {
			return actor, true
		}
	}
	return actorConfig{}, false
}

func (s *serverState) connectionResponse(actor actorConfig) dolthubauth.ConnectionResponse {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	return dolthubauth.ConnectionResponse{
		ConnectionID: actor.ConnectionID,
		SubjectID:    actor.SubjectID,
		RigHandle:    actor.Handle,
		Wastelands: []dolthubauth.WastelandConfig{
			{
				Upstream: upstreamName,
				ForkOrg:  actor.Handle,
				ForkDB:   upstreamDB,
				Mode:     "pr",
				Signing:  true,
			},
		},
		Status:        dolthubauth.StatusActive,
		RecordVersion: 1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func (s *serverState) verifyServiceRequest(w http.ResponseWriter, r *http.Request) (serviceScope, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return serviceScope{}, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	rawAuth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(rawAuth, serviceAuthPrefx) {
		http.Error(w, "missing service auth", http.StatusUnauthorized)
		return serviceScope{}, false
	}
	keyID, signature, ok := strings.Cut(strings.TrimPrefix(rawAuth, serviceAuthPrefx), ":")
	if !ok || keyID != authKeyID || signature == "" {
		http.Error(w, "invalid service auth", http.StatusUnauthorized)
		return serviceScope{}, false
	}

	timestamp := strings.TrimSpace(r.Header.Get("X-Service-Timestamp"))
	at, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || time.Since(at) > 5*time.Minute || time.Until(at) > 5*time.Minute {
		http.Error(w, "stale service auth", http.StatusUnauthorized)
		return serviceScope{}, false
	}
	nonce := strings.TrimSpace(r.Header.Get("X-Service-Nonce"))
	if nonce == "" {
		http.Error(w, "missing nonce", http.StatusUnauthorized)
		return serviceScope{}, false
	}
	bodyHash := sha256Hex(body)
	if got := strings.TrimSpace(r.Header.Get("X-Service-Body-SHA256")); got != bodyHash {
		http.Error(w, "body hash mismatch", http.StatusUnauthorized)
		return serviceScope{}, false
	}

	scope := serviceScope{
		SubjectID:    strings.TrimSpace(r.Header.Get("X-Auth-Subject-Id")),
		ConnectionID: strings.TrimSpace(r.Header.Get("X-Auth-Connection-Id")),
	}
	tenantID := strings.TrimSpace(r.Header.Get("X-Auth-Tenant-Id"))
	environment := strings.TrimSpace(r.Header.Get("X-Auth-Environment"))
	if tenantID != authTenantID || environment != authEnvironment || scope.SubjectID == "" {
		http.Error(w, "invalid auth scope", http.StatusUnauthorized)
		return serviceScope{}, false
	}

	base := strings.Join([]string{
		keyID,
		timestamp,
		nonce,
		r.Method,
		r.URL.RequestURI(),
		bodyHash,
		tenantID,
		environment,
		scope.SubjectID,
		scope.ConnectionID,
	}, "\n")
	mac := hmac.New(sha256.New, []byte(authShared))
	_, _ = mac.Write([]byte(base))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return serviceScope{}, false
	}
	return scope, true
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func newFakeDoltHubClient(baseURL string) *http.Client {
	return &http.Client{
		Transport: &rewriteTransport{baseURL: strings.TrimRight(baseURL, "/")},
		Timeout:   30 * time.Second,
	}
}

type rewriteTransport struct {
	baseURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ok := rewriteDoltHubURL(t.baseURL, req.URL.String())
	if !ok {
		return http.DefaultTransport.RoundTrip(req)
	}
	targetReq, err := http.NewRequestWithContext(req.Context(), req.Method, target, req.Body)
	if err != nil {
		return nil, err
	}
	targetReq.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(targetReq)
}

func rewriteDoltHubURL(baseURL, raw string) (string, bool) {
	switch {
	case strings.HasPrefix(raw, "https://www.dolthub.com/api/v1alpha1/"):
		suffix := strings.TrimPrefix(raw, "https://www.dolthub.com/api/v1alpha1")
		return baseURL + "/__dolthub/api/v1alpha1" + suffix, true
	case strings.HasPrefix(raw, "https://www.dolthub.com/repositories/"):
		suffix := strings.TrimPrefix(raw, "https://www.dolthub.com/repositories")
		return baseURL + "/__dolthub/repositories" + suffix, true
	default:
		return "", false
	}
}

func copyForwardHeaders(dst, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		switch canonical {
		case "Authorization", "Connection", "Host", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
			"X-Service-Timestamp", "X-Service-Nonce", "X-Service-Body-SHA256", "X-Auth-Tenant-Id", "X-Auth-Environment", "X-Auth-Subject-Id", "X-Auth-Connection-Id":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

type emptyFS struct{}

func (emptyFS) Open(_ string) (fs.File, error) {
	return nil, os.ErrNotExist
}
