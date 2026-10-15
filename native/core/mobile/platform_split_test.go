package mobile

import (
	"go/build"
	"slices"
	"testing"
)

// GOOS=android implicitly satisfies the "linux" build tag, so an Android build silently
// matched every "!windows" and "!darwin && !windows" file: the macOS networksetup DNS
// capture, /sbin/route IPv6 rejects and utun readiness, plus the do-nothing network
// placeholder. This checks the file sets an Android build really compiles, on any host,
// the same way network_windows_test.go guards the Windows build.
func TestAndroidBuildLinksOnlyAndroidPlatformFiles(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64", "arm"} {
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = "android", arch, true
		for dir, want := range map[string]struct{ in, out []string }{
			"../internal/engine": {
				in:  []string{"platform_android.go", "sysdns_android.go", "ipv6_android.go", "sysdns_common.go", "mihomo.go", "config.go", "tunspec.go", "tunfd_unix.go"},
				out: []string{"platform_unix.go", "sysdns.go", "ipv6_route.go", "platform_windows.go", "sysdns_windows.go", "ipv6_windows.go"},
			},
			"../internal/network": {
				in:  []string{"network_android.go", "host.go", "network.go"},
				out: []string{"network_other.go", "network_darwin.go", "network_windows.go"},
			},
		} {
			pkg, err := ctx.ImportDir(dir, 0)
			if err != nil {
				t.Fatalf("%s/%s: %v", dir, arch, err)
			}
			for _, f := range want.in {
				if !slices.Contains(pkg.GoFiles, f) {
					t.Errorf("android/%s %s: %s is missing from %v", arch, dir, f, pkg.GoFiles)
				}
			}
			for _, f := range want.out {
				if slices.Contains(pkg.GoFiles, f) {
					t.Errorf("android/%s %s: %s must not be linked", arch, dir, f)
				}
			}
			for _, f := range []string{"sysdns_test.go", "ipv6_route_test.go"} {
				if slices.Contains(pkg.TestGoFiles, f) {
					t.Errorf("android/%s %s: the macOS test %s would run on Android", arch, dir, f)
				}
			}
		}
	}
	// The TUN acceptance hooks exist only when asked for by tag: a normal Android bind (the
	// AAR every APK uses) must not export them.
	for _, tags := range [][]string{nil, {"asterlink_tuntest"}} {
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled, ctx.BuildTags = "android", "arm64", true, tags
		pkg, err := ctx.ImportDir(".", 0)
		if err != nil {
			t.Fatal(err)
		}
		if linked := slices.Contains(pkg.GoFiles, "tuntest.go"); linked != (tags != nil) {
			t.Errorf("tags %v: tuntest.go linked=%v", tags, linked)
		}
	}
	// The desktop builds keep their own files.
	for goos, files := range map[string][]string{
		"darwin":  {"platform_unix.go", "sysdns.go", "ipv6_route.go"},
		"windows": {"platform_windows.go", "sysdns_windows.go", "ipv6_windows.go"},
		"linux":   {"platform_unix.go", "sysdns.go", "ipv6_route.go"},
	} {
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH = goos, "arm64"
		pkg, err := ctx.ImportDir("../internal/engine", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !slices.Contains(pkg.GoFiles, f) {
				t.Errorf("%s lost %s", goos, f)
			}
		}
		if slices.Contains(pkg.GoFiles, "platform_android.go") {
			t.Errorf("%s links the Android platform file", goos)
		}
	}
}
