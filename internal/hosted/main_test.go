package hosted

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Disable the real DoltHub probe during tests — test Nango servers
	// return fake API keys that would fail validation.
	ProbeDoltHubToken = func(string) error { return nil }
	os.Exit(m.Run())
}
