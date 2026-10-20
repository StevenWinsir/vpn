package authmail

import (
	"bytes"
	"errors"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vpn/backend/internal/config"
)

func TestRegistrationEmailPolicy(t *testing.T) {
	domains := config.DefaultRegistrationDomains
	for input, want := range map[string]string{
		"  User.Name+tag@QQ.com ": "user.name+tag@qq.com", // no dot or plus rewriting
		"a@gmail.com":             "a@gmail.com",
		"A.B@Gmail.COM":           "a.b@gmail.com",
		"x@yeah.net":              "x@yeah.net",
	} {
		got, err := RegistrationEmail(input, domains)
		if err != nil || got != want {
			t.Fatalf("%q -> %q, %v; want %q", input, got, err, want)
		}
	}
	notAllowed := []string{
		"a@vip.qq.com",      // subdomain
		"a@qq.com.evil.com", // contains allowed domain
		"a@evilqq.com",      // suffix look-alike
		"a@mail-qq.com",     //
		"a@example.com",     // not listed
		"a@googlemail.com",  // alias domain
	}
	for _, input := range notAllowed {
		if _, err := RegistrationEmail(input, domains); !errors.Is(err, ErrDomainNotAllowed) {
			t.Fatalf("%q: %v, want domain not allowed", input, err)
		}
	}
	invalid := []string{
		"", "plain", "User <a@qq.com>", "a@qq.com, b@qq.com", "a@qq.com\nBcc: b@qq.com", "a\r@qq.com",
		"a@qq.com.", "a@", "@qq.com", "a@qq", "\"a b\"@qq.com", "a@[127.0.0.1]",
		"ä@qq.com", "a@qq.cоm" /* Cyrillic о */, "a@xn--qq-.com", "a\x00@qq.com", "a@@qq.com",
		strings.Repeat("a", 65) + "@qq.com",
	}
	for _, input := range invalid {
		if _, err := RegistrationEmail(input, domains); err == nil {
			t.Fatalf("%q accepted", input)
		} else if errors.Is(err, ErrDomainNotAllowed) && input != "a@xn--qq-.com" {
			// Only syntactically valid addresses may reach the domain comparison.
			t.Fatalf("%q classified as domain error, want invalid", input)
		}
	}
	// A custom list replaces (not extends) the default.
	if _, err := RegistrationEmail("a@qq.com", []string{"corp.example"}); !errors.Is(err, ErrDomainNotAllowed) {
		t.Fatal("custom list did not replace default")
	}
}

func TestHistoricalIdentityRuleUnaffectedByAllowList(t *testing.T) {
	// Login/recovery keep the historical rule: any valid address, including non-listed
	// domains and test domains, normalizes the same way as before.
	for input, want := range map[string]string{" Admin@Example.INVALID ": "admin@example.invalid", "u@corp.example.com": "u@corp.example.com"} {
		got, ok := NormalizeEmail(input)
		if !ok || got != want {
			t.Fatalf("%q -> %q %v", input, got, ok)
		}
	}
}

func TestCodeAndTokenGeneration(t *testing.T) {
	seen := map[string]bool{}
	firstDigit := map[byte]int{}
	for i := 0; i < 5000; i++ {
		code, err := NewCode()
		if err != nil || !ValidCodeFormat(code) {
			t.Fatalf("bad code %q %v", code, err)
		}
		seen[code] = true
		firstDigit[code[0]]++
	}
	// 5000 draws from 10^6: a fixed or sequential generator would collapse; leading
	// zeros must occur (uniform over 000000-999999).
	if len(seen) < 4900 || firstDigit['0'] < 300 {
		t.Fatalf("code distribution suspicious: unique=%d leadingZero=%d", len(seen), firstDigit['0'])
	}
	for _, bad := range []string{"12345", "1234567", "12345a", "１２３４５６", " 12345", "+12345", "-12345", "12 345", "0x1234"} {
		if ValidCodeFormat(bad) {
			t.Fatalf("code format accepted %q", bad)
		}
	}
	if !ValidCodeFormat("000000") || !ValidCodeFormat("012345") {
		t.Fatal("leading zeros rejected")
	}
	token, err := NewResetToken()
	if err != nil || !ValidTokenFormat(token) {
		t.Fatal("reset token invalid")
	}
	other, _ := NewResetToken()
	if token == other || ValidTokenFormat(token+"=") || ValidTokenFormat(token[:42]) || ValidTokenFormat(strings.Replace(token, token[:1], "+", 1)) {
		t.Fatal("token format check too loose")
	}
}

func testKeys(t *testing.T) Keys {
	t.Helper()
	k, err := DeriveKeys([]byte("unit-test-secret-unit-test-secret-32"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestDigestsAndSealing(t *testing.T) {
	k := testKeys(t)
	base := k.CodeDigest(PurposeRegister, "a@qq.com", "id-1", "012345")
	for name, other := range map[string]string{
		"purpose":   k.CodeDigest(PurposeReset, "a@qq.com", "id-1", "012345"),
		"email":     k.CodeDigest(PurposeRegister, "b@qq.com", "id-1", "012345"),
		"challenge": k.CodeDigest(PurposeRegister, "a@qq.com", "id-2", "012345"),
		"code":      k.CodeDigest(PurposeRegister, "a@qq.com", "id-1", "012346"),
		"shifted":   k.CodeDigest(PurposeRegister, "a@qq.co", "mid-1", "012345"),
	} {
		if other == base {
			t.Fatalf("digest ignores %s", name)
		}
	}
	other, _ := DeriveKeys([]byte("another-secret-another-secret-32by"))
	if other.CodeDigest(PurposeRegister, "a@qq.com", "id-1", "012345") == base || k.TokenDigest(PurposeReset, "t") == k.RateKey(PurposeReset, "t") {
		t.Fatal("keys not independent")
	}
	if _, err := DeriveKeys(make([]byte, 31)); err == nil {
		t.Fatal("short secret accepted")
	}
	sealed, err := k.Seal("job-1", TemplateRegisterCode, []byte("secret 012345"))
	if err != nil || bytes.Contains(sealed, []byte("012345")) {
		t.Fatal("payload not encrypted")
	}
	if plain, err := k.Open("job-1", TemplateRegisterCode, sealed); err != nil || string(plain) != "secret 012345" {
		t.Fatal("round trip failed")
	}
	for name, open := range map[string]func() error{
		"job":      func() error { _, e := k.Open("job-2", TemplateRegisterCode, sealed); return e },
		"template": func() error { _, e := k.Open("job-1", TemplateResetLink, sealed); return e },
		"key":      func() error { _, e := other.Open("job-1", TemplateRegisterCode, sealed); return e },
		"tamper": func() error {
			bad := append([]byte(nil), sealed...)
			bad[len(bad)-1] ^= 1
			_, e := k.Open("job-1", TemplateRegisterCode, bad)
			return e
		},
		"short": func() error { _, e := k.Open("job-1", TemplateRegisterCode, sealed[:5]); return e },
	} {
		if open() == nil {
			t.Fatalf("payload opened with wrong %s", name)
		}
	}
}

func TestTemplatesEscapeAndBind(t *testing.T) {
	r, err := Render(TemplateRegisterCode, mailPayload{To: "a@qq.com", Code: "012345", Minutes: 10}, "https://app.example.com")
	if err != nil || !strings.Contains(r.Text, "012345") || !strings.Contains(r.HTML, "012345") || !strings.Contains(r.Text, "10 分钟") || !strings.Contains(r.Subject, "AsterLink VPN") {
		t.Fatalf("registration template incomplete: %v", err)
	}
	for _, html := range []string{r.HTML} {
		if strings.Contains(html, "<script") || strings.Contains(html, "<img") || strings.Contains(html, "http://") {
			t.Fatal("template contains scripts, tracking pixels or external resources")
		}
	}
	link := "https://app.example.com/reset-password#token=abc<script>&x=\"y\""
	r, err = Render(TemplateResetLink, mailPayload{To: "a@qq.com", Link: link, Minutes: 30}, "https://app.example.com")
	if err != nil || strings.Contains(r.HTML, "<script>") || !strings.Contains(r.Text, "app.example.com") {
		t.Fatalf("reset template not escaped: %v", err)
	}
	if _, err = Render(TemplateResetLink, mailPayload{Link: "https://evil.example.com/reset-password#token=x", Minutes: 30}, "https://app.example.com"); err == nil {
		t.Fatal("reset link on a foreign host rendered")
	}
	if _, err = Render(TemplateRegisterCode, mailPayload{Code: "12345", Minutes: 10}, "https://app.example.com"); err == nil {
		t.Fatal("malformed code rendered")
	}
	r, err = Render(TemplatePasswordChanged, mailPayload{To: "a@qq.com"}, "https://app.example.com")
	if err != nil || strings.Contains(r.Text, "token") {
		t.Fatalf("change notice wrong: %v", err)
	}
	if _, err = Render("other", mailPayload{}, "https://app.example.com"); err == nil {
		t.Fatal("unknown template rendered")
	}
}

// TestWriteTemplatePreviews renders the three templates with synthetic values for
// review (docs/auth-mail-previews). Opt-in: AUTHMAIL_PREVIEW_DIR=<dir> go test -run Preview.
func TestWriteTemplatePreviews(t *testing.T) {
	dir := os.Getenv("AUTHMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set AUTHMAIL_PREVIEW_DIR to write previews")
	}
	site := "https://app.example.com"
	for template, p := range map[string]mailPayload{
		TemplateRegisterCode:    {To: "user@example.com", Code: "012345", Minutes: 10},
		TemplateResetLink:       {To: "user@example.com", Link: site + "/reset-password#token=SYNTHETIC-PREVIEW-TOKEN-NOT-USABLE", Minutes: 30},
		TemplatePasswordChanged: {To: "user@example.com"},
	} {
		r, err := Render(template, p, site)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, template+".html"), []byte(r.HTML+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, template+".txt"), []byte("Subject: "+r.Subject+"\n\n"+r.Text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// parseMIME is shared with the SMTP tests: it decodes a raw message into headers and
// its text/plain and text/html parts.
func parseMIME(t *testing.T, raw []byte) (*mail.Message, map[string]string) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("not multipart: %q", msg.Header.Get("Content-Type"))
	}
	parts := map[string]string{}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := reader.NextPart() // NextPart decodes quoted-printable transparently
		if err != nil {
			break
		}
		var body bytes.Buffer
		_, _ = body.ReadFrom(part)
		kind, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		parts[kind] = body.String()
	}
	return msg, parts
}
