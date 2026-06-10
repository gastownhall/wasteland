package api

import (
	"net/http"
	"net/url"
	"strings"
)

// SecurityHeaders wraps a handler with standard security response headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return SecurityHeadersWithConnectSrc(next)
}

// SecurityHeadersWithConnectSrc wraps a handler with standard security response
// headers plus optional extra connect-src origins.
func SecurityHeadersWithConnectSrc(next http.Handler, extraConnectSrc ...string) http.Handler {
	connectSrc := []string{"'self'", "https://*.ingest.us.sentry.io", "https://events.gascity.com"}
	for _, origin := range extraConnectSrc {
		if normalized, ok := normalizeConnectSrcOrigin(origin); ok {
			connectSrc = append(connectSrc, normalized)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; connect-src "+strings.Join(connectSrc, " ")+"; worker-src 'self' blob:; img-src 'self' data:")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func normalizeConnectSrcOrigin(origin string) (string, bool) {
	trimmed := strings.TrimSpace(origin)
	if trimmed == "" {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host, true
}
