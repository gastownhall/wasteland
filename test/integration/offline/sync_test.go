//go:build integration

package offline

import (
	"strings"
	"testing"
)

func TestSyncFromUpstream(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Verify seed data is in the fork clone (local_dir from config).
			cfg := env.loadConfig(t, upstream)
			forkDir := cfg["local_dir"].(string)

			raw := doltSQL(t, forkDir, "SELECT COUNT(*) FROM wanted WHERE id='w-seed001'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "1" {
				t.Fatal("seed data not found in fork after join")
			}

			// Push new data to the upstream remote store.
			newDataSQL := `INSERT INTO wanted (id, title, status, type, priority, effort_level, created_at, updated_at)
VALUES ('w-new0001', 'New upstream item', 'open', 'bug', 1, 'small', NOW(), NOW());
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Add new upstream data');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, newDataSQL)

			// Verify fork does NOT have the new data yet.
			raw = doltSQL(t, forkDir, "SELECT COUNT(*) FROM wanted WHERE id='w-new0001'")
			rows = parseCSV(t, raw)
			if len(rows) >= 2 && rows[1][0] != "0" {
				t.Fatal("fork should not have new data before sync")
			}

			// Run wl sync.
			stdout, stderr, err := runWL(t, env, "sync")
			if err != nil {
				t.Fatalf("wl sync failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Verify fork now has the new data.
			raw = doltSQL(t, forkDir, "SELECT COUNT(*) FROM wanted WHERE id='w-new0001'")
			rows = parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "1" {
				t.Errorf("fork should have new data after sync; got count=%s", rows[1][0])
			}
		})
	}
}

func TestSyncSameVersion(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Push new data without changing the schema version.
			newDataSQL := `INSERT INTO wanted (id, title, status, type, priority, effort_level, created_at, updated_at)
VALUES ('w-same0001', 'Same version item', 'open', 'feature', 2, 'medium', NOW(), NOW());
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Add data without version change');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, newDataSQL)

			// Sync should succeed without any schema version message.
			stdout, stderr, err := runWL(t, env, "sync")
			if err != nil {
				t.Fatalf("wl sync failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if strings.Contains(stdout, "Schema updated") || strings.Contains(stdout, "Schema upgraded") {
				t.Errorf("expected no schema version message when versions match, got: %s", stdout)
			}

			// Data should have synced.
			cfg := env.loadConfig(t, upstream)
			forkDir := cfg["local_dir"].(string)
			raw := doltSQL(t, forkDir, "SELECT COUNT(*) FROM wanted WHERE id='w-same0001'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "1" {
				t.Errorf("fork should have new data after sync; got count=%s", rows[1][0])
			}
		})
	}
}

func TestSyncLocalAhead(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Bump the LOCAL schema version ahead of upstream (simulates
			// a newer version of the program having run locally).
			cfg := env.loadConfig(t, upstream)
			forkDir := cfg["local_dir"].(string)
			doltSQLScript(t, env, forkDir,
				"UPDATE _meta SET value = '2.0' WHERE `key` = 'schema_version';\n"+
					"CALL DOLT_ADD('-A');\n"+
					"CALL DOLT_COMMIT('-m', 'Local bump to 2.0');\n")

			// Sync should succeed (local ahead is not an error) and note it.
			stdout, stderr, err := runWL(t, env, "sync")
			if err != nil {
				t.Fatalf("wl sync failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "ahead") {
				t.Errorf("expected 'ahead' note in output, got: %s", stdout)
			}
		})
	}
}

func TestSyncMinorVersionBump(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Bump upstream schema version from 1.2 → 1.3 (minor).
			bumpSQL := `UPDATE _meta SET value = '1.3' WHERE ` + "`key`" + ` = 'schema_version';
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Bump schema to 1.3');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, bumpSQL)

			// Sync should succeed and mention the schema update.
			stdout, stderr, err := runWL(t, env, "sync")
			if err != nil {
				t.Fatalf("wl sync failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "Schema updated") {
				t.Errorf("expected 'Schema updated' in output, got: %s", stdout)
			}
			if !strings.Contains(stdout, "backwards-compatible") {
				t.Errorf("expected 'backwards-compatible' in output, got: %s", stdout)
			}
		})
	}
}

func TestSyncMajorVersionBlocked(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Bump upstream schema version from 1.2 → 2.0 (major).
			bumpSQL := `UPDATE _meta SET value = '2.0' WHERE ` + "`key`" + ` = 'schema_version';
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Bump schema to 2.0');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, bumpSQL)

			// Sync without --upgrade should fail.
			stdout, _, err := runWL(t, env, "sync")
			if err == nil {
				t.Fatal("expected wl sync to fail on major version bump without --upgrade")
			}

			if !strings.Contains(stdout, "upgrade") {
				t.Errorf("expected upgrade instructions in output, got: %s", stdout)
			}
		})
	}
}

func TestSyncMajorVersionWithUpgrade(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Bump upstream schema version from 1.2 → 2.0 (major).
			bumpSQL := `UPDATE _meta SET value = '2.0' WHERE ` + "`key`" + ` = 'schema_version';
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Bump schema to 2.0');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, bumpSQL)

			// Sync with --upgrade should succeed.
			stdout, stderr, err := runWL(t, env, "sync", "--upgrade")
			if err != nil {
				t.Fatalf("wl sync --upgrade failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "Schema upgraded") {
				t.Errorf("expected 'Schema upgraded' in output, got: %s", stdout)
			}
		})
	}
}

func TestSyncDryRun(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			cfg := env.loadConfig(t, upstream)
			forkDir := cfg["local_dir"].(string)

			// Push new data to upstream.
			newDataSQL := `INSERT INTO wanted (id, title, status, type, priority, effort_level, created_at, updated_at)
VALUES ('w-dry0001', 'Dry run item', 'open', 'feature', 2, 'medium', NOW(), NOW());
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Add dry run data');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, newDataSQL)

			// Run wl sync --dry-run.
			stdout, stderr, err := runWL(t, env, "sync", "--dry-run")
			if err != nil {
				t.Fatalf("wl sync --dry-run failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Output should mention dry run.
			if !strings.Contains(stdout, "Dry run") {
				t.Errorf("output should mention dry run: %s", stdout)
			}

			// Fork should NOT have the new data.
			raw := doltSQL(t, forkDir, "SELECT COUNT(*) FROM wanted WHERE id='w-dry0001'")
			rows := parseCSV(t, raw)
			if len(rows) >= 2 && rows[1][0] != "0" {
				t.Errorf("fork should NOT have new data after dry-run sync; got count=%s", rows[1][0])
			}
		})
	}
}

func TestSyncDryRunMajorVersion(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			cfg := env.loadConfig(t, upstream)
			forkDir := cfg["local_dir"].(string)

			// Bump upstream schema version from 1.2 → 2.0 (major).
			bumpSQL := `UPDATE _meta SET value = '2.0' WHERE ` + "`key`" + ` = 'schema_version';
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Bump schema to 2.0');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, bumpSQL)

			// Dry-run should NOT block on major version — it should warn
			// and still show the diff so the user can evaluate.
			stdout, stderr, err := runWL(t, env, "sync", "--dry-run")
			if err != nil {
				t.Fatalf("wl sync --dry-run should not fail on major bump: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "upgrade") {
				t.Errorf("expected upgrade warning in dry-run output, got: %s", stdout)
			}
			if !strings.Contains(stdout, "Dry run") {
				t.Errorf("expected 'Dry run' in output, got: %s", stdout)
			}

			// Fork should NOT have pulled the data (dry-run).
			raw := doltSQL(t, forkDir, "SELECT value FROM _meta WHERE `key` = 'schema_version'")
			rows := parseCSV(t, raw)
			if len(rows) >= 2 && rows[1][0] != "1.2" {
				t.Errorf("fork schema_version should still be 1.2 after dry-run, got %s", rows[1][0])
			}
		})
	}
}

func TestSyncMissingSchemaVersion(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			cfg := env.loadConfig(t, upstream)
			forkDir := cfg["local_dir"].(string)

			// Delete the schema_version row from the local fork to simulate
			// a pre-versioned database.
			doltSQLScript(t, env, forkDir,
				"DELETE FROM _meta WHERE `key` = 'schema_version';\n"+
					"CALL DOLT_ADD('-A');\n"+
					"CALL DOLT_COMMIT('-m', 'Remove schema_version (pre-versioned db)');\n")

			// Push new data upstream (version stays at 1.2 there).
			newDataSQL := `INSERT INTO wanted (id, title, status, type, priority, effort_level, created_at, updated_at)
VALUES ('w-nover001', 'No-version item', 'open', 'feature', 2, 'medium', NOW(), NOW());
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Add data to versioned upstream');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, newDataSQL)

			// Sync should succeed without gating — missing version is non-fatal.
			stdout, stderr, err := runWL(t, env, "sync")
			if err != nil {
				t.Fatalf("wl sync failed with missing schema_version: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// No schema messages expected.
			if strings.Contains(stdout, "Schema updated") || strings.Contains(stdout, "Schema upgraded") {
				t.Errorf("expected no schema message with missing version, got: %s", stdout)
			}

			// Data should have synced through.
			raw := doltSQL(t, forkDir, "SELECT COUNT(*) FROM wanted WHERE id='w-nover001'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "1" {
				t.Errorf("fork should have new data after sync; got count=%s", rows[1][0])
			}
		})
	}
}

func TestSyncCorruptSchemaVersion(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)
			env.createUpstreamStoreWithData(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Corrupt the upstream schema_version to an unparseable value.
			corruptSQL := `UPDATE _meta SET value = 'abc' WHERE ` + "`key`" + ` = 'schema_version';
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('-m', 'Corrupt schema_version');
`
			env.pushToUpstreamStore(t, upstreamOrg, upstreamDB, corruptSQL)

			// Sync should succeed — corrupt version is non-fatal, sync proceeds.
			stdout, stderr, err := runWL(t, env, "sync")
			if err != nil {
				t.Fatalf("wl sync should not fail on corrupt schema_version: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Should not crash or mention schema changes.
			if strings.Contains(stdout, "Schema updated") || strings.Contains(stdout, "Schema upgraded") {
				t.Errorf("expected no schema message with corrupt version, got: %s", stdout)
			}
		})
	}
}
