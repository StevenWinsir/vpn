package referral

import (
	"bytes"
	"strings"
	"testing"
)

// R1-02: one million codes are all well formed, collisions are counted (and expected
// to be at most a handful), and every character appears about equally often.
func TestGenerateMillion(t *testing.T) {
	if testing.Short() {
		t.Skip("one million codes")
	}
	const n = 1_000_000
	seen := make(map[string]struct{}, n)
	counts := map[byte]int{}
	collisions := 0
	for i := 0; i < n; i++ {
		code, err := Generate(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !Valid(code) {
			t.Fatalf("invalid code %q", code)
		}
		if _, dup := seen[code]; dup {
			collisions++
		}
		seen[code] = struct{}{}
		for j := 0; j < len(code); j++ {
			counts[code[j]]++
		}
	}
	// Expected collisions ≈ n²/(2·31⁸) ≈ 0.6.
	if collisions > 10 {
		t.Fatalf("%d collisions in %d codes", collisions, n)
	}
	expected := float64(n*CodeLength) / float64(len(Alphabet))
	for i := 0; i < len(Alphabet); i++ {
		got := float64(counts[Alphabet[i]])
		if got < expected*0.95 || got > expected*1.05 {
			t.Fatalf("character %c appears %v times, expected about %v", Alphabet[i], got, expected)
		}
	}
	t.Logf("1,000,000 codes: %d collisions; character frequency within ±5%% of %.0f", collisions, expected)
}

// Rejection sampling: bytes 248..255 are skipped, so the mapping has no modulo bias.
func TestGenerateRejectsBiasedBytes(t *testing.T) {
	src := bytes.NewReader(append(bytes.Repeat([]byte{255, 248}, 8), 0, 1, 2, 3, 30, 31, 61, 247, 9, 9, 9, 9, 9, 9, 9, 9))
	code, err := Generate(src)
	if err != nil {
		t.Fatal(err)
	}
	if code != "2345Z2ZZ" {
		t.Fatalf("code %q", code)
	}
	if _, err := Generate(bytes.NewReader([]byte{1, 2})); err == nil {
		t.Fatal("short random source accepted")
	}
}

func TestAlphabet(t *testing.T) {
	if len(Alphabet) != 31 || strings.ContainsAny(Alphabet, "01ILO") {
		t.Fatalf("alphabet %q", Alphabet)
	}
}

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"ABCD2345", "ABCD2345", true},
		{"  abcd2345 ", "ABCD2345", true},
		{"abcd-2345", "ABCD2345", true},
		{"AB CD 23 45", "ABCD2345", true},
		{"ab　cd2345", "ABCD2345", true},
		{"", "", false},
		{"ABCD234", "", false},
		{"ABCD23456", "", false},
		{"ABCD2340", "", false},
		{"ABCD234O", "", false},
		{"ABCD2341", "", false},
		{"ABCD234I", "", false},
		{"ABCD234L", "", false},
		{"ABCD234_", "", false},
		{"ＡBCD2345", "", false},
		{strings.Repeat(" ", 60) + "ABCD2345", "", false},
		{strings.Repeat(" ", 56) + "ABCD2345", "ABCD2345", true},
	} {
		got, ok := Normalize(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("Normalize(%q) = %q %v, want %q %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"zhangsan@qq.com":   "zh***@qq.com",
		"ab@gmail.com":      "ab***@gmail.com",
		"a@163.com":         "a***@163.com",
		"张三丰@example.com":   "张三***@example.com",
		"broken":            "***",
		"@nolocal.com":      "***",
		"x.y+tag@sub.qq.cn": "x.***@sub.qq.cn",
	} {
		if got := MaskEmail(in); got != want {
			t.Fatalf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRewardBytes(t *testing.T) {
	for _, tc := range []struct {
		cents int64
		cap   int
		want  int64
	}{
		{2900, 0, 29 * GiB},
		{3990, 0, 3990 * GiB / 100},
		{1, 0, 10737418},
		{0, 0, 0},
		{-5, 0, 0},
		{100000, 50, 50 * GiB},
		{2900, 50, 29 * GiB},
	} {
		if got := RewardBytes(tc.cents, tc.cap); got != tc.want {
			t.Fatalf("RewardBytes(%d, %d) = %d, want %d", tc.cents, tc.cap, got, tc.want)
		}
	}
}
