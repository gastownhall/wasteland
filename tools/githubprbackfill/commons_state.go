package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os/exec"
	"strings"
)

type commonsState struct {
	Commit       string
	RigHandles   map[string]string
	EvidenceURLs map[string]bool
}

func loadCommonsState(dir string, allowDirty bool) (commonsState, error) {
	if dir == "" {
		return commonsState{}, fmt.Errorf("commons-dir is required")
	}
	branch, err := runDolt(dir, "branch", "--show-current")
	if err != nil {
		return commonsState{}, fmt.Errorf("checking commons branch: %w", err)
	}
	if strings.TrimSpace(branch) != "main" {
		return commonsState{}, fmt.Errorf("commons clone must be on main, got %q", strings.TrimSpace(branch))
	}
	if !allowDirty {
		status, err := runDolt(dir, "status")
		if err != nil {
			return commonsState{}, fmt.Errorf("checking commons dirtiness: %w", err)
		}
		if !strings.Contains(status, "nothing to commit, working tree clean") {
			return commonsState{}, fmt.Errorf("commons clone is dirty; pass --allow-dirty to override")
		}
	}
	commit, err := runDolt(dir, "log", "-n", "1", "--oneline")
	if err != nil {
		return commonsState{}, fmt.Errorf("reading commons commit: %w", err)
	}
	state := commonsState{
		Commit:       firstField(commit),
		RigHandles:   make(map[string]string),
		EvidenceURLs: make(map[string]bool),
	}
	rigRows, err := queryDoltCSV(dir, "SELECT handle FROM rigs")
	if err != nil {
		return commonsState{}, fmt.Errorf("query rigs: %w", err)
	}
	for _, row := range rigRows {
		handle := row["handle"]
		state.RigHandles[strings.ToLower(handle)] = handle
	}
	evidenceRows, err := queryDoltCSV(dir, "SELECT evidence FROM completions WHERE evidence LIKE 'https://github.com/%/pull/%' OR evidence LIKE 'http://github.com/%/pull/%'")
	if err != nil {
		return commonsState{}, fmt.Errorf("query completion evidence: %w", err)
	}
	for _, row := range evidenceRows {
		state.EvidenceURLs[canonicalEvidenceURL(row["evidence"])] = true
	}
	wantedRows, err := queryDoltCSV(dir, "SELECT evidence_url FROM wanted WHERE evidence_url LIKE 'https://github.com/%/pull/%' OR evidence_url LIKE 'http://github.com/%/pull/%'")
	if err != nil {
		return commonsState{}, fmt.Errorf("query wanted evidence: %w", err)
	}
	for _, row := range wantedRows {
		state.EvidenceURLs[canonicalEvidenceURL(row["evidence_url"])] = true
	}
	return state, nil
}

func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func queryDoltCSV(dir, query string) ([]map[string]string, error) {
	out, err := runDolt(dir, "sql", "-r", "csv", "-q", query)
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(strings.NewReader(out))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}
	if len(records) < 1 {
		return nil, nil
	}
	header := records[0]
	var rows []map[string]string
	for _, rec := range records[1:] {
		row := make(map[string]string)
		for i, col := range header {
			if i < len(rec) {
				row[col] = rec[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func runDolt(dir string, args ...string) (string, error) {
	cmd := exec.Command("dolt", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("dolt %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func canonicalEvidenceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimSuffix(raw, "/")
	if strings.HasPrefix(raw, "github.com/") {
		return "https://" + raw
	}
	return strings.TrimSpace(raw)
}
