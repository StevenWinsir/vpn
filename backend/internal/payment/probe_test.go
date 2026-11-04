package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// answering is a backend that answers the probe with the given secret.
func answering(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != NotifyPath || !ValidProbeChallenge(r.URL.Query().Get("probe")) {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"probe": ProbeAnswer(secret, r.URL.Query().Get("probe"))})
	}
}

// The 2026-11-03 incident: PAYMENT_NOTIFY_URL pointed at an old deployment of this
// backend, which answered every callback 200 ("unknown order"), so the gateway counted
// it delivered and the real backend never heard of the payment. The self-check tells
// that address apart from the right one.
func TestCheckNotifyURL(t *testing.T) {
	const secret = "test-only-gateway-secret"
	ctx := context.Background()
	serve := func(h http.Handler) string {
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return s.URL + NotifyPath
	}
	oldDeployment := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"code":200,"message":"处理成功"}`))
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"接口不存在"}}`))
	})
	cases := map[string]struct {
		url  string
		want string
	}{
		"this backend":                {serve(answering(secret)), ProbeOK},
		"old deployment (404 on GET)": {serve(oldDeployment), ProbeWrongTarget},
		"another secret":              {serve(answering("another-gateway-secret")), ProbeWrongTarget},
		"echoes a fixed answer": {serve(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"probe":"` + ProbeAnswer(secret, "00000000000000000000000000000000") + `"}`))
		})), ProbeWrongTarget},
		"redirect (website, http→https)": {serve(http.RedirectHandler("https://example.invalid"+NotifyPath, http.StatusMovedPermanently)), ProbeWrongTarget},
		"server error": {serve(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) })), ProbeUnreachable},
	}
	for name, c := range cases {
		if got := CheckNotifyURL(ctx, nil, c.url, secret); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
	closed := httptest.NewServer(answering(secret))
	closed.Close()
	if got := CheckNotifyURL(ctx, nil, closed.URL+NotifyPath, secret); got != ProbeUnreachable {
		t.Errorf("closed server: %s", got)
	}
}

func TestProbeAnswer(t *testing.T) {
	a, err := NewProbeChallenge()
	if err != nil || !ValidProbeChallenge(a) {
		t.Fatalf("challenge %q %v", a, err)
	}
	b, _ := NewProbeChallenge()
	if a == b || ProbeAnswer("s1-secret", a) == ProbeAnswer("s1-secret", b) || ProbeAnswer("s1-secret", a) == ProbeAnswer("s2-secret", a) {
		t.Fatal("probe answer does not depend on challenge and secret")
	}
	if ProbeAnswer("s1-secret", a) != ProbeAnswer("s1-secret", a) {
		t.Fatal("probe answer not deterministic")
	}
	for _, bad := range []string{"", "ABCDEF0123456789ABCDEF0123456789", "0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdef0"} {
		if ValidProbeChallenge(bad) {
			t.Fatalf("challenge %q accepted", bad)
		}
	}
}
