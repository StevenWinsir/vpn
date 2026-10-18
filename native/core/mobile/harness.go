//go:build asterlink_harness

package mobile

import (
	"context"
	"encoding/json"
	"time"

	"asterlink/core/internal/harness"
)

// HarnessRun runs the iOS billing acceptance (handoff_app_ios.md 8.8) inside this process and
// returns its report as JSON {"cases":[{case_id,status,observed}...]}. The simulator cannot run a
// packet tunnel, so the app-side runner links a Core built with the `asterlink_harness` tag and
// calls this: the real Core and pinned Mihomo, with the controlled client's flows injected into
// Mihomo instead of arriving from a utun, against the real backend and test node on the Mac's
// loopback. configJSON is harness.Config (the backend world's facts, the sink, the sizes).
//
// It exists only in an xcframework bound with that tag (scripts/test-native-ios.py --suite
// billing builds one into a separate directory). The release audit refuses an app that links it.
func HarnessRun(configJSON string) string {
	var cfg harness.Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		out, _ := json.Marshal(map[string]any{"cases": []harness.Case{{ID: "B.setup", Status: "FAIL", Observed: "bad harness config: " + err.Error()}}})
		return string(out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	out, _ := json.Marshal(map[string]any{"cases": harness.Run(ctx, cfg)})
	return string(out)
}
