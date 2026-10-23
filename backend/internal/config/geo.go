package config

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Geo is the IP location lookup of backend/.env (handoff_payment_account.md 4, 8). With
// no URL nothing is looked up and login records show "未解析".
type Geo struct {
	// URLTemplate holds GeoIPPlaceholder where the address goes.
	URLTemplate string
	// Token is optional and only ever sent as a bearer token header.
	Token             string
	RequestsPerMinute int
}

// GeoIPPlaceholder marks the address inside GEOIP_LOOKUP_URL.
const GeoIPPlaceholder = "{ip}"

const DefaultGeoRequestsPerMinute = 30

// Enabled reports whether a lookup service is configured.
func (g Geo) Enabled() bool { return g.URLTemplate != "" }

func loadGeo(c *Config) error {
	g := &c.Geo
	var err error
	if g.RequestsPerMinute, err = strconv.Atoi(env("GEOIP_REQUESTS_PER_MINUTE", strconv.Itoa(DefaultGeoRequestsPerMinute))); err != nil || g.RequestsPerMinute < 1 || g.RequestsPerMinute > 600 {
		return errors.New("invalid GEOIP_REQUESTS_PER_MINUTE (allowed 1-600)")
	}
	g.Token = strings.TrimSpace(env("GEOIP_LOOKUP_TOKEN", ""))
	if strings.ContainsAny(g.Token, " \t\r\n\x00") || len(g.Token) > 256 {
		return errors.New("invalid GEOIP_LOOKUP_TOKEN")
	}
	raw := strings.TrimSpace(env("GEOIP_LOOKUP_URL", ""))
	if raw == "" {
		return nil
	}
	invalid := errors.New("invalid GEOIP_LOOKUP_URL (absolute HTTPS URL containing " + GeoIPPlaceholder + ")")
	if strings.Count(raw, GeoIPPlaceholder) != 1 || !isASCII(raw) || strings.Contains(raw, "#") {
		return invalid
	}
	probe, err := url.Parse(strings.Replace(raw, GeoIPPlaceholder, "192.0.2.1", 1))
	if err != nil || probe.Host == "" || probe.User != nil || probe.Opaque != "" || strings.Contains(probe.Host, GeoIPPlaceholder) {
		return invalid
	}
	// The address is substituted into the path or query only, never into the host.
	if strings.Contains(strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://"), "/", 2)[0], GeoIPPlaceholder) {
		return invalid
	}
	switch {
	case probe.Scheme == "https":
	case probe.Scheme == "http" && c.Env != "production":
	default:
		return errors.New("GEOIP_LOOKUP_URL must use HTTPS in production")
	}
	g.URLTemplate = raw
	return nil
}
