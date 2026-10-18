//go:build asterlink_harness

package harness

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestAcceptance is the host entry of the iOS billing acceptance: the same Run the simulator
// calls through mobile.HarnessRun. The suite (scripts/test-native-ios.py --suite billing) names
// the config file and where to write the report; without them the test is skipped.
func TestAcceptance(t *testing.T) {
	path, out := os.Getenv("ASTERLINK_HARNESS_CONFIG"), os.Getenv("ASTERLINK_HARNESS_REPORT")
	if path == "" || out == "" {
		t.Skip("set by scripts/test-native-ios.py --suite billing")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	cases := Run(ctx, cfg)
	data, _ := json.MarshalIndent(map[string]any{"cases": cases}, "", "  ")
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Logf("%-8s %-24s %s", c.Status, c.ID, c.Observed)
	}
}
