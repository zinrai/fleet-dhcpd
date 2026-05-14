// Package config holds the runtime DHCP configuration and loads it
// from Consul KV. The configuration is read-only from the server's
// perspective; updates are pushed by writing to the configured KV key.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sync/atomic"
	"time"
)

// raw is the on-KV JSON representation.
type raw struct {
	Subnet     string   `json:"subnet"`
	RangeStart string   `json:"range_start"`
	RangeEnd   string   `json:"range_end"`
	Router     string   `json:"router"`
	DNS        []string `json:"dns"`
	DomainName string   `json:"domain_name"`
	LeaseTime  string   `json:"lease_time"`
	BootURL    string   `json:"boot_url"`
	WebhookURL string   `json:"webhook_url"`
}

// Config is the validated runtime configuration.
type Config struct {
	Subnet     *net.IPNet
	RangeStart net.IP
	RangeEnd   net.IP
	Router     net.IP
	DNS        []net.IP
	DomainName string
	LeaseTime  time.Duration
	BootURL    string
	WebhookURL string
}

// Parse validates JSON bytes and returns a runtime Config.
func Parse(data []byte) (*Config, error) {
	var r raw
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	_, subnet, err := net.ParseCIDR(r.Subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet %q: %w", r.Subnet, err)
	}

	rangeStart := net.ParseIP(r.RangeStart)
	if rangeStart == nil || rangeStart.To4() == nil {
		return nil, fmt.Errorf("invalid range_start %q", r.RangeStart)
	}
	rangeStart = rangeStart.To4()

	rangeEnd := net.ParseIP(r.RangeEnd)
	if rangeEnd == nil || rangeEnd.To4() == nil {
		return nil, fmt.Errorf("invalid range_end %q", r.RangeEnd)
	}
	rangeEnd = rangeEnd.To4()

	if !subnet.Contains(rangeStart) || !subnet.Contains(rangeEnd) {
		return nil, fmt.Errorf("range must be within subnet %s", subnet)
	}
	if ipLess(rangeEnd, rangeStart) {
		return nil, fmt.Errorf("range_start > range_end")
	}

	router := net.ParseIP(r.Router)
	if router == nil || router.To4() == nil {
		return nil, fmt.Errorf("invalid router %q", r.Router)
	}
	router = router.To4()

	var dns []net.IP
	for _, s := range r.DNS {
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid dns %q", s)
		}
		dns = append(dns, ip.To4())
	}

	leaseTime := time.Hour
	if r.LeaseTime != "" {
		d, err := time.ParseDuration(r.LeaseTime)
		if err != nil {
			return nil, fmt.Errorf("invalid lease_time %q: %w", r.LeaseTime, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("lease_time must be positive")
		}
		leaseTime = d
	}

	if r.BootURL != "" {
		if _, err := url.Parse(r.BootURL); err != nil {
			return nil, fmt.Errorf("invalid boot_url %q: %w", r.BootURL, err)
		}
	}

	if r.WebhookURL != "" {
		u, err := url.Parse(r.WebhookURL)
		if err != nil {
			return nil, fmt.Errorf("invalid webhook_url: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("webhook_url scheme must be http or https")
		}
		if u.Host == "" {
			return nil, fmt.Errorf("webhook_url must have a host")
		}
	}

	return &Config{
		Subnet:     subnet,
		RangeStart: rangeStart,
		RangeEnd:   rangeEnd,
		Router:     router,
		DNS:        dns,
		DomainName: r.DomainName,
		LeaseTime:  leaseTime,
		BootURL:    r.BootURL,
		WebhookURL: r.WebhookURL,
	}, nil
}

// Holder holds the current Config atomically, allowing it to be
// updated by a watcher goroutine while readers fetch the current value
// without locking.
type Holder struct {
	v atomic.Value // *Config
}

// NewHolder returns a Holder seeded with c.
func NewHolder(c *Config) *Holder {
	h := &Holder{}
	h.v.Store(c)
	return h
}

// Get returns the current Config. May return nil if the holder has not
// been initialized.
func (h *Holder) Get() *Config {
	v := h.v.Load()
	if v == nil {
		return nil
	}
	return v.(*Config)
}

// Set atomically replaces the held Config.
func (h *Holder) Set(c *Config) {
	h.v.Store(c)
}

// ipLess reports whether a is strictly less than b as 4-byte IPv4.
func ipLess(a, b net.IP) bool {
	a4 := a.To4()
	b4 := b.To4()
	for i := 0; i < 4; i++ {
		if a4[i] != b4[i] {
			return a4[i] < b4[i]
		}
	}
	return false
}
