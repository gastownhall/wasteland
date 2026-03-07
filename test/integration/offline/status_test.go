//go:build integration

package offline

import (
	"strings"
	"testing"
)

func TestStatusFullLifecycle(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnvInMode(t, backend, "wild-west")

			// 1. Post as forkOrg → status shows "open" + title.
			stdout, _, err := runWL(t, env, "post",
				"--title", "Status lifecycle test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			stdout, stderr, err := runWL(t, env, "status", wantedID)
			if err != nil {
				t.Fatalf("wl status (open) failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "open") {
				t.Errorf("expected status to contain 'open', got: %s", stdout)
			}
			if !strings.Contains(stdout, "Status lifecycle test") {
				t.Errorf("expected status to contain title, got: %s", stdout)
			}

			// 2. Switch to worker-rig, claim → status shows "claimed" + claimer.
			writeConfig(t, env, upstream, "worker-rig")

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			stdout, stderr, err = runWL(t, env, "status", wantedID)
			if err != nil {
				t.Fatalf("wl status (claimed) failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "claimed") {
				t.Errorf("expected status to contain 'claimed', got: %s", stdout)
			}
			if !strings.Contains(stdout, "worker-rig") {
				t.Errorf("expected status to contain claimer 'worker-rig', got: %s", stdout)
			}

			// 3. Done with evidence → status shows "in_review" + completion section.
			_, _, err = runWL(t, env, "done", wantedID,
				"--evidence", "https://github.com/test/pr/1",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl done failed: %v", err)
			}

			stdout, stderr, err = runWL(t, env, "status", wantedID)
			if err != nil {
				t.Fatalf("wl status (in_review) failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "in_review") {
				t.Errorf("expected status to contain 'in_review', got: %s", stdout)
			}
			if !strings.Contains(stdout, "Completion") {
				t.Errorf("expected status to contain 'Completion' section, got: %s", stdout)
			}

			// 4. Switch back to forkOrg, accept → status shows "completed" + stamp.
			writeConfig(t, env, upstream, forkOrg)

			_, _, err = runWL(t, env, "accept", wantedID,
				"--quality", "4",
				"--reliability", "3",
				"--severity", "branch",
				"--skills", "go,test",
				"--message", "great work",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl accept failed: %v", err)
			}

			stdout, stderr, err = runWL(t, env, "status", wantedID)
			if err != nil {
				t.Fatalf("wl status (completed) failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "completed") {
				t.Errorf("expected status to contain 'completed', got: %s", stdout)
			}
			if !strings.Contains(stdout, "Stamp") {
				t.Errorf("expected status to contain 'Stamp' section, got: %s", stdout)
			}
		})
	}
}
