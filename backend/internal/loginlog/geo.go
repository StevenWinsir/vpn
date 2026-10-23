// Package loginlog resolves where login records came from and prunes old records
// (handoff_payment_account.md 8, 13). A login only inserts its record; this worker
// fills in the location later, so a slow or broken lookup service never touches the
// login path. Only the IP address is ever sent to the lookup service.
package loginlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"vpn/backend/internal/config"
	"vpn/backend/internal/model"
)

const (
	// Batch is how many pending records one pass claims.
	Batch = 20
	// MaxAttempts external lookups are tried for one address before the record is failed.
	MaxAttempts = 3
	// ClaimLease hides a claimed record from other instances while it is looked up.
	ClaimLease = time.Minute
	// RetryStep is multiplied by the attempt count to space out retries.
	RetryStep       = 30 * time.Second
	LookupTimeout   = 3 * time.Second
	MaxResponseSize = 4 << 10
	maxGeoText      = 120
	pruneBatch      = 1000
)

// Local reports addresses that are never sent to an external service: private,
// loopback, link-local, unspecified and carrier-grade NAT ranges.
func Local(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(addr)
}

// Valid reports a parsable, non-empty address.
func Valid(ip string) bool {
	_, err := netip.ParseAddr(ip)
	return err == nil
}

// Worker is the geo lookup and pruning loop. One instance per API process; several
// processes may run at once because records are claimed with SKIP LOCKED.
type Worker struct {
	DB     *gorm.DB
	Schema string
	Geo    config.Geo
	Client *http.Client
	Now    func() time.Time
	Logf   func(format string, args ...any)
	// lastCall paces external lookups to Geo.RequestsPerMinute.
	lastCall time.Time
}

// New builds a worker whose HTTP client has a short timeout and never follows redirects.
func New(db *gorm.DB, cfg config.Config, logf func(string, ...any)) *Worker {
	return &Worker{
		DB: db, Schema: cfg.Schema, Geo: cfg.Geo, Now: time.Now, Logf: logf,
		Client: &http.Client{Timeout: LookupTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (w *Worker) now() time.Time { return w.Now().UTC() }

func (w *Worker) table(name string) string { return `"` + w.Schema + `".` + name }

// Run resolves pending records every few seconds and prunes once an hour, until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	tick, prune := time.NewTicker(5*time.Second), time.NewTicker(time.Hour)
	defer tick.Stop()
	defer prune.Stop()
	w.pruneLogged(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := w.Resolve(ctx); err != nil && ctx.Err() == nil {
				w.Logf("login geo: resolve failed (will retry)")
			}
		case <-prune.C:
			w.pruneLogged(ctx)
		}
	}
}

func (w *Worker) pruneLogged(ctx context.Context) {
	if _, err := w.Prune(ctx); err != nil && ctx.Err() == nil {
		w.Logf("login geo: prune failed (will retry)")
	}
}

// Resolve claims one batch of pending records and fills in their location. It returns
// how many records left the pending state.
func (w *Worker) Resolve(ctx context.Context) (int, error) {
	now := w.now()
	var batch []model.LoginEvent
	err := w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("geo_status = ? AND (geo_next_at IS NULL OR geo_next_at <= ?)", model.GeoPending, now).
			Order("occurred_at").Limit(Batch).Find(&batch).Error; e != nil || len(batch) == 0 {
			return e
		}
		ids := make([]string, len(batch))
		for i, ev := range batch {
			ids[i] = ev.ID
		}
		return tx.Model(&model.LoginEvent{}).Where("id IN ?", ids).Update("geo_next_at", now.Add(ClaimLease)).Error
	})
	if err != nil {
		return 0, err
	}
	finished := 0
	for _, ev := range batch {
		if ctx.Err() != nil {
			return finished, ctx.Err()
		}
		done, e := w.settle(ctx, ev)
		if e != nil {
			return finished, e
		}
		if done {
			finished++
		}
	}
	return finished, nil
}

// settle decides one record: local address, no service, cache hit, or an external
// lookup. It reports whether the record left the pending state.
func (w *Worker) settle(ctx context.Context, ev model.LoginEvent) (bool, error) {
	finish := func(status, text string) (bool, error) {
		at := w.now()
		return true, w.DB.WithContext(ctx).Model(&model.LoginEvent{}).Where("id = ? AND geo_status = ?", ev.ID, model.GeoPending).
			Updates(map[string]any{"geo_status": status, "geo_text": text, "geo_resolved_at": at, "geo_next_at": nil}).Error
	}
	switch {
	case !Valid(ev.IP):
		return finish(model.GeoFailed, "")
	case Local(ev.IP):
		return finish(model.GeoDone, model.GeoLocal)
	case !w.Geo.Enabled():
		return finish(model.GeoSkipped, "")
	}
	var cached model.IPGeoCache
	err := w.DB.WithContext(ctx).Where("ip = ? AND expires_at > ?", ev.IP, w.now()).Take(&cached).Error
	switch {
	case err == nil && cached.GeoText != "":
		return finish(model.GeoDone, cached.GeoText)
	case err == nil:
		return finish(model.GeoFailed, "")
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return false, err
	}
	if err = w.pace(ctx); err != nil {
		return false, err
	}
	text, lookupErr := w.lookup(ctx, ev.IP)
	at := w.now()
	if lookupErr == nil {
		if err = w.remember(ctx, ev.IP, text, at, model.GeoCacheTTL); err != nil {
			return false, err
		}
		return finish(model.GeoDone, text)
	}
	attempts := ev.GeoAttempts + 1
	if attempts >= MaxAttempts {
		if err = w.remember(ctx, ev.IP, "", at, model.GeoFailureTTL); err != nil {
			return false, err
		}
		return finish(model.GeoFailed, "")
	}
	return false, w.DB.WithContext(ctx).Model(&model.LoginEvent{}).Where("id = ? AND geo_status = ?", ev.ID, model.GeoPending).
		Updates(map[string]any{"geo_attempts": attempts, "geo_next_at": at.Add(time.Duration(attempts) * RetryStep)}).Error
}

func (w *Worker) remember(ctx context.Context, ip, text string, at time.Time, ttl time.Duration) error {
	row := model.IPGeoCache{IP: ip, GeoText: text, ResolvedAt: at, ExpiresAt: at.Add(ttl)}
	return w.DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ip"}}, UpdateAll: true}).Create(&row).Error
}

// pace spaces external calls at least 60s / GEOIP_REQUESTS_PER_MINUTE apart.
func (w *Worker) pace(ctx context.Context) error {
	rate := w.Geo.RequestsPerMinute
	if rate < 1 {
		rate = config.DefaultGeoRequestsPerMinute
	}
	gap := time.Minute / time.Duration(rate)
	if wait := gap - time.Since(w.lastCall); wait > 0 && !w.lastCall.IsZero() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	w.lastCall = time.Now()
	return nil
}

// lookup asks the configured service about one address.
func (w *Worker) lookup(ctx context.Context, ip string) (string, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", err
	}
	target := strings.Replace(w.Geo.URLTemplate, config.GeoIPPlaceholder, url.PathEscape(addr.Unmap().String()), 1)
	ctx, cancel := context.WithTimeout(ctx, LookupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if w.Geo.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.Geo.Token)
	}
	resp, err := w.Client.Do(req)
	if err != nil {
		return "", errors.New("lookup request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lookup status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseSize+1))
	if err != nil || len(body) > MaxResponseSize {
		return "", errors.New("lookup response unreadable or too large")
	}
	return ParseLocation(body)
}

// ParseLocation turns a lookup response into "country region city isp". Public services
// name their fields differently, so the common spellings are accepted; a response with
// no place in it, or one that reports a failure, is an error.
func ParseLocation(body []byte) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", errors.New("lookup response is not a JSON object")
	}
	if failed(doc) {
		return "", errors.New("lookup reported a failure")
	}
	if inner, ok := doc["data"].(map[string]any); ok {
		for k, v := range inner {
			if _, exists := doc[k]; !exists {
				doc[k] = v
			}
		}
	}
	place := []string{
		pick(doc, "country_name", "countryName", "country"),
		pick(doc, "regionName", "region_name", "region", "province", "state"),
		pick(doc, "city"),
	}
	if place[0] == "" && place[1] == "" && place[2] == "" {
		return "", errors.New("lookup response has no location")
	}
	isp := pick(doc, "isp", "org", "organization", "as_name")
	if conn, ok := doc["connection"].(map[string]any); ok && isp == "" {
		isp = pick(conn, "isp", "org")
	}
	var parts []string
	for _, p := range append(place, isp) {
		if p != "" && (len(parts) == 0 || parts[len(parts)-1] != p) {
			parts = append(parts, p)
		}
	}
	return clip(strings.Join(parts, " ")), nil
}

func failed(doc map[string]any) bool {
	if s, ok := doc["status"].(string); ok && (strings.EqualFold(s, "fail") || strings.EqualFold(s, "error")) {
		return true
	}
	if ok, present := doc["success"].(bool); present && !ok {
		return true
	}
	switch e := doc["error"].(type) {
	case bool:
		return e
	case string:
		return e != ""
	case map[string]any:
		return len(e) > 0
	}
	return false
}

func pick(doc map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := doc[key].(string); ok {
			if s = clean(s); s != "" {
				return s
			}
		}
	}
	return ""
}

// clean keeps printable text only: the value comes from a third party and is shown to
// administrators and users.
func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s))
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= maxGeoText {
		return s
	}
	return string([]rune(s)[:maxGeoText])
}

// Prune deletes records past their retention: login records and callback records after
// 180 days (the latter are anonymous once their account is gone), expired cache rows.
func (w *Worker) Prune(ctx context.Context) (int64, error) {
	now := w.now()
	var total int64
	for _, step := range []struct {
		sql string
		arg time.Time
	}{
		{`DELETE FROM ` + w.table("login_events") + ` WHERE id IN (SELECT id FROM ` + w.table("login_events") + ` WHERE occurred_at < ? LIMIT ? FOR UPDATE SKIP LOCKED)`, now.Add(-model.LoginEventRetention)},
		{`DELETE FROM ` + w.table("payment_notifications") + ` WHERE id IN (SELECT id FROM ` + w.table("payment_notifications") + ` WHERE received_at < ? LIMIT ? FOR UPDATE SKIP LOCKED)`, now.Add(-model.LoginEventRetention)},
		{`DELETE FROM ` + w.table("ip_geo_caches") + ` WHERE ip IN (SELECT ip FROM ` + w.table("ip_geo_caches") + ` WHERE expires_at < ? LIMIT ? FOR UPDATE SKIP LOCKED)`, now},
	} {
		for {
			result := w.DB.WithContext(ctx).Exec(step.sql, step.arg, pruneBatch)
			if result.Error != nil {
				return total, result.Error
			}
			total += result.RowsAffected
			if result.RowsAffected < pruneBatch {
				break
			}
		}
	}
	return total, nil
}
