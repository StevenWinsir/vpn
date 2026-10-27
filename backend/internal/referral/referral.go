// Package referral holds the pure rules of the referral programme (handoff_coupon.md 4,
// D6, D10): the code alphabet and generator, input normalisation, e-mail masking and
// the reward conversion. Storage and the reward worker live in the api package.
package referral

import (
	"crypto/rand"
	"errors"
	"io"
	"math"
	"strings"
)

// Alphabet is 2-9 and A-Z without I, L and O: 31 characters that are hard to misread.
const Alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// CodeLength gives 31^8 ≈ 8.5e11 codes.
const CodeLength = 8

// MaxInputLength: anything longer is ignored without looking at the database.
const MaxInputLength = 64

// GiB is the traffic unit of the whole site.
const GiB = int64(1) << 30

var errShortRandom = errors.New("referral: random source exhausted")

// Generate draws one code from r (crypto/rand.Reader when nil) by rejection sampling,
// so every character is equally likely.
func Generate(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	// 248 = 8 × 31 is the largest multiple of 31 below 256.
	const limit = 256 - 256%len(Alphabet)
	out := make([]byte, 0, CodeLength)
	buf := make([]byte, 16)
	for len(out) < CodeLength {
		n, err := io.ReadFull(r, buf)
		if err != nil {
			return "", errShortRandom
		}
		for _, b := range buf[:n] {
			if int(b) >= limit {
				continue
			}
			out = append(out, Alphabet[int(b)%len(Alphabet)])
			if len(out) == CodeLength {
				break
			}
		}
	}
	return string(out), nil
}

// Valid reports whether code is a normalised code: CodeLength characters of Alphabet.
func Valid(code string) bool {
	if len(code) != CodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if strings.IndexByte(Alphabet, code[i]) < 0 {
			return false
		}
	}
	return true
}

// Normalize turns user input into a code: surrounding and inner spaces and hyphens are
// dropped and letters upper-cased. ok is false for anything that cannot be a code
// (including empty input); such input is ignored silently, never reported.
func Normalize(raw string) (code string, ok bool) {
	if len(raw) > MaxInputLength {
		return "", false
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == ' ' || r == '-' || r == '\t' || r == '　':
			continue
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r < 128:
			b.WriteRune(r)
		default:
			return "", false
		}
	}
	code = b.String()
	if !Valid(code) {
		return "", false
	}
	return code, true
}

// MaskEmail keeps the first two characters of the local part (one when it is a single
// character), replaces the rest with "***" and keeps the whole domain:
// zhangsan@qq.com -> zh***@qq.com.
func MaskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	local, domain := []rune(email[:at]), email[at+1:]
	keep := min(2, len(local))
	if len(local) == 1 {
		keep = 1
	}
	return string(local[:keep]) + "***@" + domain
}

// RewardBytes converts paid cents into traffic at 1 yuan = 1 GiB, rounded down to the
// byte, then applies the optional cap (maxGiB 0 = none). Non-positive cents give 0.
func RewardBytes(cents int64, maxGiB int) int64 {
	if cents <= 0 {
		return 0
	}
	var bytes int64
	if cents > math.MaxInt64/GiB {
		bytes = math.MaxInt64
	} else {
		bytes = cents * GiB / 100
	}
	if maxGiB > 0 {
		bytes = min(bytes, int64(maxGiB)*GiB)
	}
	return bytes
}
