// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/discover.go).
// The UEFI HTTP Boot detection branch has been removed; only iPXE
// detection (Option 77 User Class) is retained.

package dhcp

import (
	"context"

	"go.universe.tf/netboot/dhcp4"
)

// isIPXEBoot reports whether the request is from an iPXE client.
// RFC 3004 Option 77 (User Class) carries the string "iPXE".
func isIPXEBoot(pkt *dhcp4.Packet) bool {
	ucls, err := pkt.Options.String(77)
	if err != nil {
		return false
	}
	return ucls == "iPXE"
}

func (h *Handler) handleDiscover(ctx context.Context, pkt *dhcp4.Packet, intf netIface) (*dhcp4.Packet, error) {
	serverAddr, err := getIPv4AddrForInterface(intf)
	if err != nil {
		return nil, err
	}

	yourip, err := h.store.Allocate(ctx, pkt.HardwareAddr)
	if err != nil {
		return nil, err
	}

	opts, err := h.makeOptions()
	if err != nil {
		return nil, err
	}
	opts[dhcp4.OptServerIdentifier] = serverAddr

	resp := &dhcp4.Packet{
		Type:          dhcp4.MsgOffer,
		TransactionID: pkt.TransactionID,
		Broadcast:     pkt.Broadcast,
		HardwareAddr:  pkt.HardwareAddr,
		YourAddr:      yourip,
		ServerAddr:    serverAddr,
		RelayAddr:     pkt.RelayAddr,
		Options:       opts,
	}

	if isIPXEBoot(pkt) {
		cfg := h.config.Get()
		if cfg != nil && cfg.BootURL != "" {
			h.log.Info("iPXE client; setting boot URL",
				"chaddr", pkt.HardwareAddr.String(),
				"yiaddr", yourip.String(),
				"boot_url", cfg.BootURL,
			)
			resp.BootFilename = cfg.BootURL
		}
	}

	return resp, nil
}
