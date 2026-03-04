package api

import (
	"io/fs"
	"net/http"
	"strings"
)

// SPAHandler serves static files from the embedded filesystem with
// index.html fallback for client-side routing. API routes are excluded.
func SPAHandler(apiHandler http.Handler, assets fs.FS) http.Handler {
	// Try to access the dist directory from the embed.
	distFS, err := fs.Sub(assets, "dist")
	if err != nil {
		return spaFallback(apiHandler)
	}

	// Check if dist has any content.
	entries, err := fs.ReadDir(distFS, ".")
	if err != nil || len(entries) == 0 {
		return spaFallback(apiHandler)
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API routes go to the API handler.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}

		// Try to serve the static file.
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Check if the file exists in the embedded FS.
		f, err := distFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			_ = f.Close()
			setCacheHeaders(w, path)
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for client-side routing.
		// No cache — must revalidate to pick up new deploys.
		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// setCacheHeaders sets Cache-Control based on whether the asset is
// content-hashed (immutable) or not. Vite produces hashed filenames in
// /assets/ — these never change and can be cached indefinitely. index.html
// and other non-hashed files must revalidate to pick up new deploys.
func setCacheHeaders(w http.ResponseWriter, path string) {
	if strings.HasPrefix(path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
}

// spaFallback returns a handler that serves the API and shows a "not built"
// message for all non-API routes.
func spaFallback(apiHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Web UI not built. Run 'make web' to build, or use 'wl serve --dev' with Vite dev server.\n"))
	})
}
