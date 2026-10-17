package managed

import "testing"

func TestClientPlatformComesFromTheBuildTarget(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "macos", "windows": "windows", "android": "android", "ios": "ios", "linux": "linux", "freebsd": "linux"} {
		if got := clientPlatformFor(goos); got != want {
			t.Errorf("%s -> %q, want %q", goos, got, want)
		}
	}
	if ClientPlatform() == "" {
		t.Fatal("empty platform")
	}
}
