// Command webserver serves the embedded web UI against the e2e harness
// backend so browser automation can drive the full stack without a real
// wl binary.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const defaultAddr = "127.0.0.1:4173"

func main() {
	addr := flag.String("addr", defaultAddr, "listen address")
	dir := flag.String("dir", "./web/dist", "static asset directory")
	apiBase := flag.String("api", "http://127.0.0.1:8999", "upstream api base URL")
	flag.Parse()

	upstream, err := url.Parse(*apiBase)
	if err != nil {
		log.Fatalf("parse api base URL: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	assetDir := http.Dir(*dir)

	mux := http.NewServeMux()
	mux.Handle("/api/", proxy)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveSPA(assetDir, *dir, w, r)
	})

	log.Printf("e2e web server listening on %s, serving %s and proxying /api to %s", *addr, *dir, upstream)
	if err := http.ListenAndServe(*addr, mux); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve e2e web app: %v", err)
	}
}

func serveSPA(fsRoot http.Dir, distDir string, w http.ResponseWriter, r *http.Request) {
	cleanPath := path.Clean("/" + r.URL.Path)
	trimmed := strings.TrimPrefix(cleanPath, "/")
	if trimmed == "." {
		trimmed = ""
	}

	if trimmed != "" {
		fullPath := filepath.Join(distDir, filepath.FromSlash(trimmed))
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			http.ServeFile(w, r, fullPath)
			return
		}
		if path.Ext(trimmed) != "" {
			http.NotFound(w, r)
			return
		}
	}

	indexPath := filepath.Join(string(fsRoot), "index.html")
	http.ServeFile(w, r, indexPath)
}
