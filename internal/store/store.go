// Package store defines the lease persistence interface and the
// observer interface for lease state changes. A Consul KV-backed
// implementation is provided in this package.
package store

import (
	"context"
	"net"
	"time"
)

// LeaseStore abstracts persistent lease state. Implementations must
// handle concurrent access from multiple fleet-dhcpd instances; the
// bundled ConsulStore uses Consul KV CAS transactions to serialize
// writes.
type LeaseStore interface {
	// Allocate returns an IP address for the given hardware address.
	// If the client already has a binding, the same IP is returned.
	// Otherwise a new IP is chosen from the configured range. The lease
	// expiration is renewed.
	Allocate(ctx context.Context, hwaddr net.HardwareAddr) (net.IP, error)

	// Renew confirms an existing lease for hwaddr on ip. It returns an
	// error if no matching lease exists.
	Renew(ctx context.Context, ip net.IP, hwaddr net.HardwareAddr) error

	// Release removes the lease for hwaddr on ip.
	Release(ctx context.Context, ip net.IP, hwaddr net.HardwareAddr) error

	// Decline marks ip as unusable, preventing it from being offered to
	// other clients.
	Decline(ctx context.Context, ip net.IP, hwaddr net.HardwareAddr) error
}

// EventType identifies the kind of lease state change.
type EventType string

const (
	EventOffer   EventType = "offer"
	EventAck     EventType = "ack"
	EventRelease EventType = "release"
	EventDecline EventType = "decline"
)

// LeaseEvent describes a lease state change. Implementations of
// LeaseObserver receive these events after the corresponding
// LeaseStore operation has committed.
type LeaseEvent struct {
	Type      EventType
	IP        net.IP
	MAC       net.HardwareAddr
	ExpiresAt time.Time
	Timestamp time.Time
}

// LeaseObserver receives notifications when lease state changes.
// Implementations must be safe for concurrent invocation and should not
// block the caller (the lease operation has already committed by the
// time the observer is invoked, but the DHCP response is waiting).
//
// A nil LeaseObserver passed to a LeaseStore is treated as "no observer";
// the store must not panic and must skip notification.
type LeaseObserver interface {
	OnLeaseChange(event LeaseEvent)
}
