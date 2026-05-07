package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func manifestHash(m Manifest) (string, error) {
	m.Hash = ""
	canonicalizeManifest(&m)
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal canonical manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func readManifest(path string) (Manifest, error) {
	data, err := readFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return m, nil
}

func validateManifest(m Manifest) error {
	if m.Version != backfillVersion {
		return fmt.Errorf("manifest version = %q, want %q", m.Version, backfillVersion)
	}
	if m.FormulaVersion != formulaVersion {
		return fmt.Errorf("formula version = %q, want %q", m.FormulaVersion, formulaVersion)
	}
	for _, pr := range m.PRs {
		if pr.Decision == "stamp" {
			if pr.Subject == "" {
				return fmt.Errorf("%s#%d stamped PR has empty subject", pr.Repo, pr.Number)
			}
			if err := validateScore(pr.Score); err != nil {
				return fmt.Errorf("%s#%d: %w", pr.Repo, pr.Number, err)
			}
		}
	}
	return nil
}

func canonicalizeManifest(m *Manifest) {
	sort.Strings(m.Inputs.Repos)
	sort.Slice(m.IdentityMappings, func(i, j int) bool {
		return strings.ToLower(m.IdentityMappings[i].GitHubLogin) < strings.ToLower(m.IdentityMappings[j].GitHubLogin)
	})
	sort.Slice(m.PRs, func(i, j int) bool {
		if m.PRs[i].Repo == m.PRs[j].Repo {
			return m.PRs[i].Number < m.PRs[j].Number
		}
		return m.PRs[i].Repo < m.PRs[j].Repo
	})
	sort.Slice(m.SyntheticRows.Rigs, func(i, j int) bool {
		return m.SyntheticRows.Rigs[i].Handle < m.SyntheticRows.Rigs[j].Handle
	})
	sort.Slice(m.SyntheticRows.Wanted, func(i, j int) bool {
		return m.SyntheticRows.Wanted[i].ID < m.SyntheticRows.Wanted[j].ID
	})
	sort.Slice(m.SyntheticRows.Completions, func(i, j int) bool {
		return m.SyntheticRows.Completions[i].ID < m.SyntheticRows.Completions[j].ID
	})
	sort.Slice(m.SyntheticRows.Stamps, func(i, j int) bool {
		return m.SyntheticRows.Stamps[i].ID < m.SyntheticRows.Stamps[j].ID
	})
	sort.Strings(m.Warnings)
}
