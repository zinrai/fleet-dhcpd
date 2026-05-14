// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/handler.go).
// The IPAM-driven makeOptions has been replaced with a direct read from
// configuration. Logging migrated to log/slog.

package dhcp

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"

	"go.universe.tf/netboot/dhcp4"

	"github.com/zinrai/fleet-dhcpd/internal/config"
	"github.com/zinrai/fleet-dhcpd/internal/store"
)

// Handler is the DHCP message dispatcher.
type Handler struct {
	store  store.LeaseStore
	config *config.Holder
	log    *slog.Logger
}

// NewHandler returns a Handler bound to the given LeaseStore and config.
func NewHandler(s store.LeaseStore, cfg *config.Holder, log *slog.Logger) *Handler {
	return &Handler{store: s, config: cfg, log: log}
}

// ServeDHCP dispatches based on DHCP message type.
func (h *Handler) ServeDHCP(ctx context.Context, pkt *dhcp4.Packet, intf netIface) (*dhcp4.Packet, error) {
	switch pkt.Type {
	case dhcp4.MsgDiscover:
		return h.handleDiscover(ctx, pkt, intf)
	case dhcp4.MsgRequest:
		return h.handleRequest(ctx, pkt, intf)
	case dhcp4.MsgDecline:
		return h.handleDecline(ctx, pkt, intf)
	case dhcp4.MsgRelease:
		return h.handleRelease(ctx, pkt, intf)
	case dhcp4.MsgInform:
		return h.handleInform(ctx, pkt, intf)
	default:
		h.log.Error("unexpected DHCP message type", "type", pkt.Type.String())
	}
	return nil, errUnknownMsgType
}

func getIPv4AddrForInterface(intf netIface) (net.IP, error) {
	addrs, err := intf.Addrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		ipaddr, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ipaddr4 := ipaddr.IP.To4()
		if ipaddr4 != nil {
			return ipaddr4, nil
		}
	}
	return nil, errors.New("no IPv4 address for " + intf.Name())
}

func flattenIPv4s(ips []net.IP) []byte {
	buf := make([]byte, 0, 4*len(ips))
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		buf = append(buf, v4...)
	}
	return buf
}

// makeOptions returns dhcp4.Options that includes the common DHCP options
// derived from configuration:
//
//   - Subnet Mask (1)
//   - Router (3)
//   - Domain Name Server (6)
//   - Domain Name (15)
//   - Lease Time (51)
//
// The Server Identifier (54) is set by callers.
func (h *Handler) makeOptions() (dhcp4.Options, error) {
	cfg := h.config.Get()
	if cfg == nil {
		return nil, errors.New("no configuration available")
	}

	opts := make(dhcp4.Options)

	opts[dhcp4.OptSubnetMask] = cfg.Subnet.Mask
	opts[dhcp4.OptRouters] = cfg.Router.To4()

	if len(cfg.DNS) > 0 {
		opts[dhcp4.OptDNSServers] = flattenIPv4s(cfg.DNS)
	}

	if cfg.DomainName != "" {
		opts[dhcp4.OptDomainName] = []byte(cfg.DomainName)
	}

	secs := uint32(cfg.LeaseTime.Seconds())
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, secs)
	opts[dhcp4.OptLeaseTime] = buf

	return opts, nil
}
