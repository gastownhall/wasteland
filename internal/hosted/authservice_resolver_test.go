package hosted

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gastownhall/wasteland/internal/dolthubauth"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestAuthServiceWorkspaceResolver_InvalidateConnectionClearsPendingCaches(t *testing.T) {
	resolver := NewAuthServiceWorkspaceResolver(nil, NewSessionStore())
	cache1 := newPendingUpstreamCache(nil, "hop", "wl-commons", time.Hour)
	cache2 := newPendingUpstreamCache(nil, "gastownhall", "gascity", time.Hour)
	defer cache1.Stop()
	defer cache2.Stop()

	resolver.pendingCache["conn-1:hop/wl-commons"] = cache1
	resolver.pendingCache["conn-2:gastownhall/gascity"] = cache2

	resolver.InvalidateConnection("conn-1")

	if _, ok := resolver.pendingCache["conn-1:hop/wl-commons"]; ok {
		t.Fatal("expected conn-1 pending cache to be evicted")
	}
	if _, ok := resolver.pendingCache["conn-2:gastownhall/gascity"]; !ok {
		t.Fatal("expected unrelated pending cache to remain")
	}
}

func TestAuthServiceWorkspaceResolver_BuildClientWarmsPendingCache(t *testing.T) {
	resolver := NewAuthServiceWorkspaceResolver(nil, NewSessionStore())
	session := &UserSession{
		SubjectID:    "subject-1",
		ConnectionID: "conn-1",
	}
	conn := &dolthubauth.ConnectionResponse{
		ConnectionID: "conn-1",
		SubjectID:    "subject-1",
		RigHandle:    "alice",
	}
	wl := dolthubauth.WastelandConfig{
		Upstream: "hop/wl-commons",
		ForkOrg:  "alice",
		ForkDB:   "wl-commons",
		Mode:     "pr",
	}

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}

	if _, err := resolver.buildClient(session, conn, client, wl); err != nil {
		t.Fatalf("buildClient() error = %v", err)
	}

	key := "conn-1:hop/wl-commons"
	cache, ok := resolver.pendingCache[key]
	if !ok {
		t.Fatalf("expected pending cache %q to be created during build", key)
	}
	cache.Stop()
}
