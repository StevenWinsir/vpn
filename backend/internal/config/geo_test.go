package config

import "testing"

// The geo lookup group: absent by default, HTTPS only in production, one {ip}
// placeholder that never sits in the host, bounded rate.
func TestGeoConfiguration(t *testing.T) {
	paymentBase(t, false)
	for _, k := range []string{"GEOIP_LOOKUP_URL", "GEOIP_LOOKUP_TOKEN", "GEOIP_REQUESTS_PER_MINUTE"} {
		t.Setenv(k, "")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Geo.Enabled() || c.Geo.RequestsPerMinute != DefaultGeoRequestsPerMinute {
		t.Fatalf("defaults wrong: %+v", c.Geo)
	}
	t.Setenv("GEOIP_LOOKUP_URL", "https://geo.example.invalid/json/{ip}?lang=zh-CN")
	t.Setenv("GEOIP_LOOKUP_TOKEN", "token-value")
	t.Setenv("GEOIP_REQUESTS_PER_MINUTE", "120")
	if c, err = Load(); err != nil || !c.Geo.Enabled() || c.Geo.Token != "token-value" || c.Geo.RequestsPerMinute != 120 {
		t.Fatalf("valid configuration rejected: %v %+v", err, c.Geo)
	}
	t.Setenv("GEOIP_LOOKUP_URL", "http://geo.example.invalid/{ip}")
	if _, err = Load(); err != nil {
		t.Fatalf("plain HTTP is allowed outside production: %v", err)
	}
	for _, bad := range []string{"https://geo.example.invalid/json", "https://geo.example.invalid/{ip}/{ip}", "https://{ip}.example.invalid/", "https://user:pw@geo.example.invalid/{ip}", "geo.example.invalid/{ip}", "https://geo.example.invalid/{ip}#x", "ftp://geo.example.invalid/{ip}"} {
		t.Setenv("GEOIP_LOOKUP_URL", bad)
		if _, err = Load(); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	for _, rate := range []string{"0", "601", "x"} {
		t.Setenv("GEOIP_LOOKUP_URL", "")
		t.Setenv("GEOIP_REQUESTS_PER_MINUTE", rate)
		if _, err = Load(); err == nil {
			t.Fatalf("rate %q was accepted", rate)
		}
	}
	t.Setenv("GEOIP_REQUESTS_PER_MINUTE", "30")
	paymentBase(t, true)
	t.Setenv("GEOIP_LOOKUP_URL", "http://geo.example.invalid/{ip}")
	t.Setenv("GEOIP_REQUESTS_PER_MINUTE", "")
	if _, err = Load(); err == nil {
		t.Fatal("production accepted plain HTTP")
	}
	t.Setenv("GEOIP_LOOKUP_URL", "https://geo.example.invalid/{ip}")
	if _, err = Load(); err != nil {
		t.Fatalf("production HTTPS rejected: %v", err)
	}
}
