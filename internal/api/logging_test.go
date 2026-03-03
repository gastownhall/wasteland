package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusRecorder_CapturesExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	sr.WriteHeader(http.StatusNotFound)
	if sr.status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", sr.status, http.StatusNotFound)
	}
}

func TestStatusRecorder_DefaultsTo200OnWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	_, _ = sr.Write([]byte("hello"))
	if sr.status != http.StatusOK {
		t.Fatalf("status = %d, want %d", sr.status, http.StatusOK)
	}
}

func TestStatusRecorder_IgnoresDoubleWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	sr.WriteHeader(http.StatusCreated)
	sr.WriteHeader(http.StatusNotFound) // should be ignored
	if sr.status != http.StatusCreated {
		t.Fatalf("status = %d, want %d", sr.status, http.StatusCreated)
	}
}

func TestRequestLog_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestLog(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, buf.String())
	}

	if entry["method"] != "GET" {
		t.Errorf("method = %v, want GET", entry["method"])
	}
	if entry["path"] != "/api/wanted" {
		t.Errorf("path = %v, want /api/wanted", entry["path"])
	}
	// JSON numbers decode as float64.
	if status, ok := entry["status"].(float64); !ok || int(status) != 200 {
		t.Errorf("status = %v, want 200", entry["status"])
	}
	if entry["client_ip"] != "10.0.0.1" {
		t.Errorf("client_ip = %v, want 10.0.0.1", entry["client_ip"])
	}
	if entry["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", entry["level"])
	}
}

func TestRequestLog_5xxLogsAtError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	handler := RequestLog(logger)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/fail", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, buf.String())
	}

	if entry["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", entry["level"])
	}
	if status, ok := entry["status"].(float64); !ok || int(status) != 500 {
		t.Errorf("status = %v, want 500", entry["status"])
	}
}

func TestRequestLog_ImplicitOKStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok")) // no explicit WriteHeader
	})
	handler := RequestLog(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, buf.String())
	}

	if status, ok := entry["status"].(float64); !ok || int(status) != 200 {
		t.Errorf("status = %v, want 200", entry["status"])
	}
}
