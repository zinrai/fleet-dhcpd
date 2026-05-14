// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// The lease-tracking design (single key holding all leases, CAS retry
// loop, declined entries stored alongside live leases) is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/models/etcd/dhcp.go).
// The persistence layer has been rewritten for Consul KV CAS.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/zinrai/fleet-dhcpd/internal/config"
)

// declinedPrefix is the prefix used to record declined IPs alongside live
// leases. A real MAC address never starts with these bytes, so it cannot
// collide with a valid lease key.
const declinedPrefix = "declined:"

// maxCASRetries bounds the CAS retry loop. With healthy Consul and modest
// concurrency, completion in 1-2 attempts is typical. The bound prevents
// pathological busy loops when Consul is unreachable or the lease set is
// being thrashed.
const maxCASRetries = 32

// leaseRecord is the per-lease JSON entry stored inside the leases key.
type leaseRecord struct {
	IP        string    `json:"ip"`
	ExpiresAt time.Time `json:"expires_at"`
}

// leaseSet is the full set of leases serialized into a single Consul KV
// value. The map key is either a MAC address string (live lease) or a
// "declined:<ip>" pseudo-key.
type leaseSet map[string]leaseRecord

func (ls leaseSet) inUse(ip net.IP, now time.Time) bool {
	target := ip.To4().String()
	for _, l := range ls {
		if l.IP != target {
			continue
		}
		if now.Before(l.ExpiresAt) {
			return true
		}
	}
	return false
}

func (ls leaseSet) gc(now time.Time) {
	for k, l := range ls {
		if !now.Before(l.ExpiresAt) {
			delete(ls, k)
		}
	}
}

// ConsulStore is a LeaseStore that keeps state in Consul KV. All leases
// for a cluster live in a single key (leasesKey) and are updated via
// CAS to serialize concurrent writes across instances.
type ConsulStore struct {
	client    *api.Client
	leasesKey string
	config    *config.Holder
	log       *slog.Logger
	observer  LeaseObserver
}

// NewConsulStore returns a ConsulStore that uses the given Consul client
// and KV prefix. observer may be nil to disable notifications.
func NewConsulStore(client *api.Client, kvPrefix string, cfg *config.Holder, log *slog.Logger, observer LeaseObserver) *ConsulStore {
	return &ConsulStore{
		client:    client,
		leasesKey: path.Join(kvPrefix, "leases"),
		config:    cfg,
		log:       log,
		observer:  observer,
	}
}

// readLeases fetches the current leaseSet from Consul along with its
// ModifyIndex. An empty result is returned with index 0 when the key
// does not exist yet.
func (s *ConsulStore) readLeases(ctx context.Context) (leaseSet, uint64, error) {
	pair, _, err := s.client.KV().Get(s.leasesKey, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, 0, fmt.Errorf("consul get %s: %w", s.leasesKey, err)
	}
	if pair == nil {
		return make(leaseSet), 0, nil
	}
	var ls leaseSet
	if err := json.Unmarshal(pair.Value, &ls); err != nil {
		return nil, 0, fmt.Errorf("decode leases: %w", err)
	}
	if ls == nil {
		ls = make(leaseSet)
	}
	return ls, pair.ModifyIndex, nil
}

func (s *ConsulStore) writeLeasesCAS(ctx context.Context, ls leaseSet, modifyIndex uint64) (bool, error) {
	data, err := json.Marshal(ls)
	if err != nil {
		return false, fmt.Errorf("encode leases: %w", err)
	}
	pair := &api.KVPair{
		Key:         s.leasesKey,
		Value:       data,
		ModifyIndex: modifyIndex,
	}
	ok, _, err := s.client.KV().CAS(pair, (&api.WriteOptions{}).WithContext(ctx))
	if err != nil {
		return false, fmt.Errorf("consul cas: %w", err)
	}
	return ok, nil
}

func (s *ConsulStore) Allocate(ctx context.Context, hwaddr net.HardwareAddr) (net.IP, error) {
	cfg := s.config.Get()
	if cfg == nil {
		return nil, errors.New("no configuration available")
	}

	key := hwaddr.String()

	for attempt := 0; attempt < maxCASRetries; attempt++ {
		ls, idx, err := s.readLeases(ctx)
		if err != nil {
			return nil, err
		}

		now := time.Now()
		ls.gc(now)

		var chosen net.IP
		if existing, ok := ls[key]; ok {
			chosen = net.ParseIP(existing.IP).To4()
		} else {
			start := ipToUint32(cfg.RangeStart)
			end := ipToUint32(cfg.RangeEnd)
			for n := start; n <= end; n++ {
				ip := uint32ToIP(n)
				if ls.inUse(ip, now) {
					continue
				}
				chosen = ip
				break
			}
			if chosen == nil {
				return nil, errors.New("no IP available in pool")
			}
		}

		expiresAt := now.Add(cfg.LeaseTime)
		ls[key] = leaseRecord{
			IP:        chosen.String(),
			ExpiresAt: expiresAt,
		}

		ok, err := s.writeLeasesCAS(ctx, ls, idx)
		if err != nil {
			return nil, err
		}
		if ok {
			s.notify(EventOffer, chosen, hwaddr, expiresAt)
			return chosen, nil
		}
		s.log.Debug("CAS retry on Allocate", "attempt", attempt+1, "chaddr", key)
	}
	return nil, fmt.Errorf("CAS retries exhausted on Allocate")
}

func (s *ConsulStore) Renew(ctx context.Context, ip net.IP, hwaddr net.HardwareAddr) error {
	cfg := s.config.Get()
	if cfg == nil {
		return errors.New("no configuration available")
	}

	key := hwaddr.String()
	target := ip.To4().String()

	for attempt := 0; attempt < maxCASRetries; attempt++ {
		ls, idx, err := s.readLeases(ctx)
		if err != nil {
			return err
		}

		existing, ok := ls[key]
		if !ok {
			return errors.New("no lease for " + key)
		}
		if existing.IP != target {
			return fmt.Errorf("lease IP mismatch for %s: have %s, requested %s", key, existing.IP, target)
		}

		existing.ExpiresAt = time.Now().Add(cfg.LeaseTime)
		ls[key] = existing

		ok, err = s.writeLeasesCAS(ctx, ls, idx)
		if err != nil {
			return err
		}
		if ok {
			s.notify(EventAck, ip, hwaddr, existing.ExpiresAt)
			return nil
		}
		s.log.Debug("CAS retry on Renew", "attempt", attempt+1, "chaddr", key)
	}
	return fmt.Errorf("CAS retries exhausted on Renew")
}

func (s *ConsulStore) Release(ctx context.Context, ip net.IP, hwaddr net.HardwareAddr) error {
	key := hwaddr.String()
	target := ip.To4().String()

	for attempt := 0; attempt < maxCASRetries; attempt++ {
		ls, idx, err := s.readLeases(ctx)
		if err != nil {
			return err
		}

		existing, ok := ls[key]
		if !ok {
			return nil
		}
		if existing.IP != target {
			return fmt.Errorf("lease IP mismatch on release: have %s, requested %s", existing.IP, target)
		}

		delete(ls, key)

		ok, err = s.writeLeasesCAS(ctx, ls, idx)
		if err != nil {
			return err
		}
		if ok {
			s.notify(EventRelease, ip, hwaddr, time.Time{})
			return nil
		}
		s.log.Debug("CAS retry on Release", "attempt", attempt+1, "chaddr", key)
	}
	return fmt.Errorf("CAS retries exhausted on Release")
}

func (s *ConsulStore) Decline(ctx context.Context, ip net.IP, hwaddr net.HardwareAddr) error {
	key := hwaddr.String()
	declinedKey := declinedPrefix + ip.To4().String()

	for attempt := 0; attempt < maxCASRetries; attempt++ {
		ls, idx, err := s.readLeases(ctx)
		if err != nil {
			return err
		}

		// Drop any live lease for this MAC.
		delete(ls, key)

		ls[declinedKey] = leaseRecord{
			IP:        ip.To4().String(),
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}

		ok, err := s.writeLeasesCAS(ctx, ls, idx)
		if err != nil {
			return err
		}
		if ok {
			s.notify(EventDecline, ip, hwaddr, time.Time{})
			return nil
		}
		s.log.Debug("CAS retry on Decline", "attempt", attempt+1, "chaddr", key)
	}
	return fmt.Errorf("CAS retries exhausted on Decline")
}

// notify dispatches a lease event to the observer if one is configured.
// Called only after a successful CAS, so each lease change is notified
// exactly once across the cluster.
func (s *ConsulStore) notify(typ EventType, ip net.IP, hwaddr net.HardwareAddr, expiresAt time.Time) {
	if s.observer == nil {
		return
	}
	s.observer.OnLeaseChange(LeaseEvent{
		Type:      typ,
		IP:        ip,
		MAC:       hwaddr,
		ExpiresAt: expiresAt,
		Timestamp: time.Now().UTC(),
	})
}
