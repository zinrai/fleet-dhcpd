package webhook

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zinrai/fleet-dhcpd/internal/store"
)

type captureServer struct {
	*httptest.Server
	mu     sync.Mutex
	events []map[string]any
}

func newCaptureServer(t *testing.T) *captureServer {
	t.Helper()
	c := &captureServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		c.mu.Lock()
		c.events = append(c.events, got)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return c
}

func (c *captureServer) wait(t *testing.T, n int, d time.Duration) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := len(c.events)
		c.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.events))
	copy(out, c.events)
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNotifierSendsEvent(t *testing.T) {
	srv := newCaptureServer(t)
	defer srv.Close()

	n := New(srv.URL, discardLogger())
	hw, _ := net.ParseMAC("52:54:00:aa:bb:cc")
	n.OnLeaseChange(store.LeaseEvent{
		Type:      store.EventOffer,
		IP:        net.IPv4(192, 168, 10, 100),
		MAC:       hw,
		Timestamp: time.Now().UTC(),
	})

	events := srv.wait(t, 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0]["ip"] != "192.168.10.100" {
		t.Errorf("ip: got %v", events[0]["ip"])
	}
	if events[0]["type"] != "offer" {
		t.Errorf("type: got %v", events[0]["type"])
	}
}

func TestNotifierNilIsNoOp(t *testing.T) {
	// Must not panic.
	var n *Notifier
	n.OnLeaseChange(store.LeaseEvent{Type: store.EventOffer})
}

func TestNotifierEmptyURLIsNoOp(t *testing.T) {
	srv := newCaptureServer(t)
	defer srv.Close()

	n := New("", discardLogger())
	n.OnLeaseChange(store.LeaseEvent{
		Type: store.EventOffer,
		IP:   net.IPv4(192, 168, 10, 100),
	})

	time.Sleep(50 * time.Millisecond)
	events := srv.wait(t, 0, 50*time.Millisecond)
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty URL, got %d", len(events))
	}
}

func TestNotifierServerErrorDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := New(srv.URL, discardLogger())
	n.OnLeaseChange(store.LeaseEvent{
		Type: store.EventOffer,
		IP:   net.IPv4(192, 168, 10, 100),
	})
	time.Sleep(100 * time.Millisecond)
}
