package store

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/zinrai/fleet-dhcpd/internal/config"
)

const testConfigJSON = `{
  "subnet": "192.168.10.0/24",
  "range_start": "192.168.10.100",
  "range_end": "192.168.10.200",
  "router": "192.168.10.1",
  "dns": ["1.1.1.1", "8.8.8.8"],
  "domain_name": "lab.local",
  "lease_time": "1h"
}`

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// requireConsul returns a Consul API client if Consul is reachable.
// If Consul is not available the test is skipped.
//
// If CONSUL_HTTP_ADDR is unset and the default 'localhost' resolves to
// both IPv4 and IPv6, the Go resolver may pick IPv6 first and fail when
// Consul binds only to 127.0.0.1. Pin to 127.0.0.1 to avoid this in
// local development; CI and operators can override with CONSUL_HTTP_ADDR.
func requireConsul(t *testing.T) *api.Client {
	t.Helper()
	cfg := api.DefaultConfig()
	if os.Getenv("CONSUL_HTTP_ADDR") == "" {
		cfg.Address = "127.0.0.1:8500"
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		t.Skipf("Consul not available: %v", err)
	}
	if _, err := client.Status().Leader(); err != nil {
		t.Skipf("Consul not reachable: %v", err)
	}
	return client
}

func newTestPrefix(t *testing.T) string {
	t.Helper()
	return path.Join("fleet-dhcpd-test", t.Name(), fmt.Sprintf("%d", time.Now().UnixNano()))
}

func cleanupPrefix(t *testing.T, client *api.Client, prefix string) {
	t.Helper()
	_, err := client.KV().DeleteTree(prefix, nil)
	if err != nil {
		t.Logf("cleanup of %s failed: %v", prefix, err)
	}
}

func newTestStore(t *testing.T) (*ConsulStore, *api.Client, string) {
	t.Helper()
	client := requireConsul(t)
	prefix := newTestPrefix(t)
	t.Cleanup(func() { cleanupPrefix(t, client, prefix) })

	cfg, err := config.Parse([]byte(testConfigJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	holder := config.NewHolder(cfg)
	s := NewConsulStore(client, prefix, holder, discardLogger(), nil)
	return s, client, prefix
}

func TestConsulStoreAllocate(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	hw, _ := net.ParseMAC("52:54:00:11:11:11")
	ip, err := s.Allocate(ctx, hw)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !ip.Equal(net.IPv4(192, 168, 10, 100)) {
		t.Errorf("first IP: got %s, want 192.168.10.100", ip)
	}
}

func TestConsulStoreAllocateStickyAcrossClients(t *testing.T) {
	client := requireConsul(t)
	prefix := newTestPrefix(t)
	t.Cleanup(func() { cleanupPrefix(t, client, prefix) })

	cfg, _ := config.Parse([]byte(testConfigJSON))
	holder := config.NewHolder(cfg)

	s1 := NewConsulStore(client, prefix, holder, discardLogger(), nil)
	s2 := NewConsulStore(client, prefix, holder, discardLogger(), nil)
	ctx := context.Background()

	hw, _ := net.ParseMAC("52:54:00:11:11:11")
	ip1, err := s1.Allocate(ctx, hw)
	if err != nil {
		t.Fatalf("s1.Allocate: %v", err)
	}
	ip2, err := s2.Allocate(ctx, hw)
	if err != nil {
		t.Fatalf("s2.Allocate: %v", err)
	}
	if !ip1.Equal(ip2) {
		t.Errorf("same MAC got different IPs across instances: %s vs %s", ip1, ip2)
	}
}

func TestConsulStoreConcurrentAllocate(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	const N = 10
	results := make([]net.IP, N)
	errs := make([]error, N)
	var wg sync.WaitGroup

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hw := net.HardwareAddr{0x52, 0x54, 0x00, 0x00, 0x00, byte(i)}
			results[i], errs[i] = s.Allocate(ctx, hw)
		}(i)
	}
	wg.Wait()

	seen := make(map[string]int)
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Errorf("client %d: %v", i, errs[i])
			continue
		}
		key := results[i].String()
		if prev, ok := seen[key]; ok {
			t.Errorf("clients %d and %d both got %s", prev, i, key)
		}
		seen[key] = i
	}
	if t.Failed() {
		return
	}
	if len(seen) != N {
		t.Errorf("got %d distinct IPs, want %d", len(seen), N)
	}
}

func TestConsulStoreRenew(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	hw, _ := net.ParseMAC("52:54:00:11:11:11")
	ip, _ := s.Allocate(ctx, hw)

	if err := s.Renew(ctx, ip, hw); err != nil {
		t.Errorf("Renew: %v", err)
	}
}

func TestConsulStoreRenewMismatchFails(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	hw, _ := net.ParseMAC("52:54:00:11:11:11")
	s.Allocate(ctx, hw)

	wrong := net.IPv4(192, 168, 10, 250)
	if err := s.Renew(ctx, wrong, hw); err == nil {
		t.Error("expected error on IP mismatch")
	}
}

func TestConsulStoreRelease(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	hw, _ := net.ParseMAC("52:54:00:11:11:11")
	ip, _ := s.Allocate(ctx, hw)
	if err := s.Release(ctx, ip, hw); err != nil {
		t.Fatalf("Release: %v", err)
	}

	ip2, _ := s.Allocate(ctx, hw)
	if !ip2.Equal(net.IPv4(192, 168, 10, 100)) {
		t.Errorf("after release, got %s; expected 192.168.10.100", ip2)
	}
}

func TestConsulStoreDeclineBlocksIP(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	hw1, _ := net.ParseMAC("52:54:00:11:11:11")
	ip1, _ := s.Allocate(ctx, hw1)
	if err := s.Decline(ctx, ip1, hw1); err != nil {
		t.Fatalf("Decline: %v", err)
	}

	hw2, _ := net.ParseMAC("52:54:00:22:22:22")
	ip2, _ := s.Allocate(ctx, hw2)
	if ip2.Equal(ip1) {
		t.Errorf("declined IP %s was reoffered to another client", ip1)
	}
}

func TestConsulStorePoolExhausted(t *testing.T) {
	client := requireConsul(t)
	prefix := newTestPrefix(t)
	t.Cleanup(func() { cleanupPrefix(t, client, prefix) })

	tinyConfig := `{
		"subnet": "192.168.10.0/24",
		"range_start": "192.168.10.100",
		"range_end": "192.168.10.101",
		"router": "192.168.10.1",
		"lease_time": "1h"
	}`
	cfg, _ := config.Parse([]byte(tinyConfig))
	holder := config.NewHolder(cfg)
	s := NewConsulStore(client, prefix, holder, discardLogger(), nil)
	ctx := context.Background()

	hw1, _ := net.ParseMAC("52:54:00:00:00:01")
	hw2, _ := net.ParseMAC("52:54:00:00:00:02")
	hw3, _ := net.ParseMAC("52:54:00:00:00:03")

	if _, err := s.Allocate(ctx, hw1); err != nil {
		t.Fatalf("hw1: %v", err)
	}
	if _, err := s.Allocate(ctx, hw2); err != nil {
		t.Fatalf("hw2: %v", err)
	}
	if _, err := s.Allocate(ctx, hw3); err == nil {
		t.Fatal("expected exhaustion error on 3rd Allocate")
	}
}

// captureObserver collects LeaseEvents for inspection.
type captureObserver struct {
	mu     sync.Mutex
	events []LeaseEvent
}

func (c *captureObserver) OnLeaseChange(ev LeaseEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *captureObserver) snapshot() []LeaseEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LeaseEvent, len(c.events))
	copy(out, c.events)
	return out
}

func TestConsulStoreObserverReceivesEvents(t *testing.T) {
	client := requireConsul(t)
	prefix := newTestPrefix(t)
	t.Cleanup(func() { cleanupPrefix(t, client, prefix) })

	cfg, _ := config.Parse([]byte(testConfigJSON))
	holder := config.NewHolder(cfg)
	obs := &captureObserver{}
	s := NewConsulStore(client, prefix, holder, discardLogger(), obs)
	ctx := context.Background()

	hw, _ := net.ParseMAC("52:54:00:11:11:11")
	ip, _ := s.Allocate(ctx, hw)
	s.Renew(ctx, ip, hw)
	s.Release(ctx, ip, hw)

	events := obs.snapshot()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	wantTypes := []EventType{EventOffer, EventAck, EventRelease}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("event[%d] type: got %q, want %q", i, events[i].Type, want)
		}
	}
}

func TestConsulStoreNilObserverIsNoOp(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	// observer is nil; must not panic.
	hw, _ := net.ParseMAC("52:54:00:11:11:11")
	if _, err := s.Allocate(ctx, hw); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
}
