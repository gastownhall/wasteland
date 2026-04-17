package githubcache

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestResolver returns a resolver pointed at srv.URL so tests can
// exercise the real HTTP pipeline without touching api.github.com.
func newTestResolver(srv *httptest.Server) *httpResolver {
	return &httpResolver{
		client:  &http.Client{Timeout: 5 * time.Second},
		baseURL: srv.URL,
	}
}

func TestResolvePRAuthorSuccess(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if got, want := r.URL.Path, "/repos/gastownhall/gascity/pulls/548"; got != want {
			t.Errorf("path: got %q, want %q", got, want)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header: got %q", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
			t.Errorf("X-GitHub-Api-Version: got %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "wasteland-github-cache/1" {
			t.Errorf("User-Agent: got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"login":"alice"}}`))
	}))
	defer srv.Close()

	r := newTestResolver(srv)
	got, err := r.ResolvePRAuthor(context.Background(),
		"https://github.com/gastownhall/gascity/pull/548")
	if err != nil {
		t.Fatalf("ResolvePRAuthor: %v", err)
	}
	if got != "alice" {
		t.Fatalf("author: got %q, want alice", got)
	}
	if !strings.HasPrefix(sawAuth, "Bearer ") {
		t.Fatalf("Authorization header: got %q, want Bearer prefix", sawAuth)
	}
}

func TestResolvePRAuthor404(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	r := newTestResolver(srv)
	_, err := r.ResolvePRAuthor(context.Background(),
		"https://github.com/o/r/pull/1")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error missing 404: %v", err)
	}
}

func TestResolvePRAuthorMissingLogin(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{}}`))
	}))
	defer srv.Close()

	r := newTestResolver(srv)
	_, err := r.ResolvePRAuthor(context.Background(),
		"https://github.com/o/r/pull/1")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "user.login") {
		t.Fatalf("error should mention user.login: %v", err)
	}
}

func TestResolvePRAuthorNoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("HTTP call should not happen when token is unset (path=%s)", r.URL.Path)
	}))
	defer srv.Close()

	r := newTestResolver(srv)
	_, err := r.ResolvePRAuthor(context.Background(),
		"https://github.com/o/r/pull/1")
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err: got %v, want ErrNoToken", err)
	}
}

func TestResolvePRAuthorNonPRURL(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("HTTP call should not happen for non-PR URL (path=%s)", r.URL.Path)
	}))
	defer srv.Close()

	r := newTestResolver(srv)
	_, err := r.ResolvePRAuthor(context.Background(),
		"https://github.com/o/r/commit/abc123")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "not a GitHub PR URL") {
		t.Fatalf("error should mention non-PR URL: %v", err)
	}
}
