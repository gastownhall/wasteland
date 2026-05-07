package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type identityOverride struct {
	Handle string `json:"handle"`
	Reason string `json:"reason"`
}

func loadIdentityOverrides(path string) (map[string]identityOverride, error) {
	if path == "" {
		return nil, fmt.Errorf("identity-overrides is required")
	}
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]identityOverride
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse identity overrides: %w", err)
	}
	for login, ov := range out {
		if ov.Handle == "" || ov.Reason == "" {
			return nil, fmt.Errorf("identity override %q requires handle and reason", login)
		}
	}
	return out, nil
}

type scoreOverride struct {
	Quality     int     `json:"quality"`
	Reliability int     `json:"reliability"`
	Creativity  int     `json:"creativity"`
	Severity    string  `json:"severity"`
	Confidence  float64 `json:"confidence"`
	Reason      string  `json:"reason"`
}

func loadScoreOverrides(path string) (map[string]scoreOverride, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]scoreOverride
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse score overrides: %w", err)
	}
	for key, ov := range out {
		score := Score{
			Quality:     ov.Quality,
			Reliability: ov.Reliability,
			Creativity:  ov.Creativity,
			Severity:    ov.Severity,
			Confidence:  ov.Confidence,
		}
		if ov.Reason == "" {
			return nil, fmt.Errorf("score override %q requires reason", key)
		}
		if err := validateScore(score); err != nil {
			return nil, fmt.Errorf("score override %q: %w", key, err)
		}
	}
	return out, nil
}

func loadExcludedLogins(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err == nil {
		out := make(map[string]string, len(raw))
		for login, reason := range raw {
			login = strings.ToLower(strings.TrimSpace(login))
			reason = strings.TrimSpace(reason)
			if login == "" || reason == "" {
				return nil, fmt.Errorf("excluded login entries require login and reason")
			}
			out[login] = reason
		}
		return out, nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse excluded logins: %w", err)
	}
	out := make(map[string]string, len(list))
	for _, login := range list {
		login = strings.ToLower(strings.TrimSpace(login))
		if login == "" {
			return nil, fmt.Errorf("excluded login cannot be empty")
		}
		out[login] = "skipped_excluded_maintainer"
	}
	return out, nil
}

func excludedLoginList(excluded map[string]string) []string {
	out := make([]string, 0, len(excluded))
	for login := range excluded {
		out = append(out, login)
	}
	sort.Strings(out)
	return out
}
