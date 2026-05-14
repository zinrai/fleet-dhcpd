package config

import (
	"testing"
	"time"
)

const validConfigJSON = `{
  "subnet": "192.168.10.0/24",
  "range_start": "192.168.10.100",
  "range_end": "192.168.10.200",
  "router": "192.168.10.1",
  "dns": ["1.1.1.1", "8.8.8.8"],
  "domain_name": "lab.local",
  "lease_time": "1h",
  "boot_url": "http://boot.example.com/ipxe",
  "webhook_url": ""
}`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validConfigJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if cfg.Subnet.String() != "192.168.10.0/24" {
		t.Errorf("subnet: got %s", cfg.Subnet)
	}
	if cfg.RangeStart.String() != "192.168.10.100" {
		t.Errorf("range_start: got %s", cfg.RangeStart)
	}
	if cfg.RangeEnd.String() != "192.168.10.200" {
		t.Errorf("range_end: got %s", cfg.RangeEnd)
	}
	if cfg.Router.String() != "192.168.10.1" {
		t.Errorf("router: got %s", cfg.Router)
	}
	if len(cfg.DNS) != 2 {
		t.Errorf("dns count: got %d, want 2", len(cfg.DNS))
	}
	if cfg.LeaseTime != time.Hour {
		t.Errorf("lease_time: got %v, want 1h", cfg.LeaseTime)
	}
	if cfg.BootURL != "http://boot.example.com/ipxe" {
		t.Errorf("boot_url: got %s", cfg.BootURL)
	}
}

func TestParseRejectsRangeOutsideSubnet(t *testing.T) {
	bad := `{
		"subnet": "192.168.10.0/24",
		"range_start": "192.168.11.100",
		"range_end": "192.168.11.200",
		"router": "192.168.10.1"
	}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for range outside subnet")
	}
}

func TestParseRejectsReversedRange(t *testing.T) {
	bad := `{
		"subnet": "192.168.10.0/24",
		"range_start": "192.168.10.200",
		"range_end": "192.168.10.100",
		"router": "192.168.10.1"
	}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for reversed range")
	}
}

func TestParseRejectsInvalidLeaseTime(t *testing.T) {
	bad := `{
		"subnet": "192.168.10.0/24",
		"range_start": "192.168.10.100",
		"range_end": "192.168.10.200",
		"router": "192.168.10.1",
		"lease_time": "not-a-duration"
	}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for invalid lease_time")
	}
}

func TestParseRejectsInvalidWebhookScheme(t *testing.T) {
	bad := `{
		"subnet": "192.168.10.0/24",
		"range_start": "192.168.10.100",
		"range_end": "192.168.10.200",
		"router": "192.168.10.1",
		"webhook_url": "ftp://example.com/hook"
	}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for non-http(s) webhook scheme")
	}
}

func TestHolderAtomicUpdate(t *testing.T) {
	c1, _ := Parse([]byte(validConfigJSON))
	h := NewHolder(c1)

	if h.Get() != c1 {
		t.Errorf("Get returned different pointer than stored")
	}

	c2, _ := Parse([]byte(validConfigJSON))
	h.Set(c2)

	if h.Get() != c2 {
		t.Errorf("Get did not return updated value")
	}
}
