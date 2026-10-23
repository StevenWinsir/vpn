package managed

import "log"

// trace writes one line about a step of the connect and stop paths (state, reason and error
// codes only: never a token, address or document). On Android the AAR routes the standard log
// to logcat, which is where an acceptance failure is read from.
func trace(format string, args ...any) {
	log.Printf("asterlink-core: "+format, args...)
}
