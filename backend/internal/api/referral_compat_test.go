package api

import (
	"testing"

	"vpn/backend/internal/model"
)

// R3-03: what an app sees when its user is rewarded while connected. The reward changes the
// allowance only: the entitlement fingerprint (plan, devices, dedicated lines) stays, so the
// session is not asked to authorise again, the profile stays fetchable, and the next session
// refresh (the app's own, every report interval) shows the larger total. The apps of all four
// platforms read exactly this contract (/client/session and /client/config); their own suites
// run unchanged in CI.
func TestClientStaysConnectedWhenRewarded(t *testing.T) {
	e := newRefEnv(t, nil)
	e.writeProfiles()
	a := e.user("compat-referrer", "user")
	code := e.codeOf(a)
	e.buy(a, "starter", "")
	token := e.clientLogin(a.Email)
	e.want(e.native("GET", "/api/v1/client/config", token, nil), 200)
	before, status := e.clientSession(token)
	if status != 200 || !before.CanConnect || before.TotalBytes <= 0 {
		t.Fatalf("session before the reward: %+v %d", before, status)
	}

	// B registers with A's code and pays the example price; the reward is settled by the worker.
	b, _ := e.register(newMailbox(), code)
	order := e.buy(b, "e2e29", "")
	if e.settle() != 1 {
		t.Fatal("the reward was not settled")
	}
	granted := order.AmountCents * gibBytes / 100
	var row model.Referral
	if err := e.db.First(&row, "referee_id = ?", b.ID).Error; err != nil || row.Status != model.ReferralRewarded || row.GrantedBytes != granted {
		t.Fatalf("referral row %+v %v", row, err)
	}

	// The same client session: still connectable, not asked to authorise again, and it sees the
	// reward on its next refresh. The profile of the unchanged entitlement is still served.
	after, status := e.clientSession(token)
	if status != 200 || !after.CanConnect || after.Reason != "" {
		t.Fatalf("the reward disturbed the app: %+v %d", after, status)
	}
	if after.TotalBytes-before.TotalBytes != granted {
		t.Fatalf("total %d -> %d, want +%d", before.TotalBytes, after.TotalBytes, granted)
	}
	e.want(e.native("GET", "/api/v1/client/config", token, nil), 200)
}
