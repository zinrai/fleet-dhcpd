// Package webhook provides an HTTP webhook implementation of
// store.LeaseObserver. Delivery is asynchronous and best-effort.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/zinrai/fleet-dhcpd/internal/store"
)

// payload is the JSON shape posted to the webhook URL.
type payload struct {
	Type      store.EventType `json:"type"`
	IP        string          `json:"ip"`
	MAC       string          `json:"mac,omitempty"`
	ExpiresAt time.Time       `json:"expires_at,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// Notifier posts LeaseEvents to a configured URL.
// Notifier implements store.LeaseObserver.
type Notifier struct {
	url    string
	client *http.Client
	log    *slog.Logger
}

// New returns a Notifier. If url is empty, OnLeaseChange becomes a no-op.
func New(url string, log *slog.Logger) *Notifier {
	return &Notifier{
		url: url,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		log: log,
	}
}

// OnLeaseChange implements store.LeaseObserver. The HTTP POST is
// dispatched on a goroutine; the call returns immediately.
func (n *Notifier) OnLeaseChange(ev store.LeaseEvent) {
	if n == nil || n.url == "" {
		return
	}
	go n.post(ev)
}

func (n *Notifier) post(ev store.LeaseEvent) {
	p := payload{
		Type:      ev.Type,
		IP:        ev.IP.String(),
		MAC:       ev.MAC.String(),
		ExpiresAt: ev.ExpiresAt,
		Timestamp: ev.Timestamp,
	}

	body, err := json.Marshal(p)
	if err != nil {
		n.log.Warn("webhook marshal failed", "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		n.log.Warn("webhook request build failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "fleet-dhcpd")

	resp, err := n.client.Do(req)
	if err != nil {
		n.log.Warn("webhook POST failed", "url", n.url, "err", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		n.log.Warn("webhook returned error status",
			"url", n.url,
			"status", resp.StatusCode,
			"type", ev.Type,
		)
		return
	}
	n.log.Debug("webhook sent",
		"url", n.url,
		"type", ev.Type,
		"ip", p.IP,
	)
}
