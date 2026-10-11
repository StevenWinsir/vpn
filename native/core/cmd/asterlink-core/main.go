// Command asterlink-core is the Managed Core worker. Stage 1 exposes only the IPC
// handshake over stdin/stdout; the Helper owns the pipes. Diagnostics go to stderr
// and must never contain tokens, passwords or node configuration.
package main

import (
	"fmt"
	"os"

	"asterlink/core/internal/ipc"
)

// Set with -ldflags "-X main.version=... -X main.mihomoRevision=...".
var (
	version        = "0.0.0-dev"
	mihomoRevision = "70f0570405c3c2c47bb113b88db95006d239b346"
)

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

func main() {
	if len(os.Args) > 1 {
		if os.Args[1] == "--version" {
			fmt.Println(version, mihomoRevision)
			return
		}
		fmt.Fprintln(os.Stderr, "asterlink-core accepts no arguments")
		os.Exit(2)
	}
	if err := ipc.Serve(stdio{}, ipc.BuildInfo{CoreVersion: version, MihomoRevision: mihomoRevision}); err != nil {
		fmt.Fprintln(os.Stderr, "ipc terminated:", err)
		os.Exit(1)
	}
}
