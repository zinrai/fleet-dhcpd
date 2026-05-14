// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/request.go).

package dhcp

import (
	"context"

	"go.universe.tf/netboot/dhcp4"
)

// handleRequest handles DHCPREQUEST. There are three use-cases:
//
//  1. Accept offer from a server (SELECTING state).
//  2. Confirm previously assigned IP address (INIT-REBOOT state).
//  3. Renew/rebind lease for an already received address (RENEWING/REBINDING).
func (h *Handler) handleRequest(ctx context.Context, pkt *dhcp4.Packet, intf netIface) (*dhcp4.Packet, error) {
	serverAddr, err := getIPv4AddrForInterface(intf)
	if err != nil {
		return nil, err
	}

	serverIdentifier, err := pkt.Options.IP(dhcp4.OptServerIdentifier)
	hasServerIdentifier := err == nil

	requestedIP, err := pkt.Options.IP(dhcp4.OptRequestedIP)
	hasRequestedIP := err == nil

	if hasServerIdentifier {
		// case 1: SELECTING — client is accepting an offer.
		if !serverAddr.Equal(serverIdentifier) {
			h.log.Info("ignored request to another server",
				"chaddr", pkt.HardwareAddr.String(),
				"server_identifier", serverIdentifier.String(),
			)
			return nil, errNotChosen
		}

		h.log.Info("received response to OFFER", "chaddr", pkt.HardwareAddr.String())

		resp, err := h.handleDiscover(ctx, pkt, intf)
		if err != nil {
			return nil, err
		}
		resp.Type = dhcp4.MsgAck
		return resp, nil
	}

	if hasRequestedIP {
		// case 2: INIT-REBOOT.
		h.log.Info("requested confirmation on reboot",
			"chaddr", pkt.HardwareAddr.String(),
			"requested_ip", requestedIP.String(),
		)

		err = h.store.Renew(ctx, requestedIP, pkt.HardwareAddr)
		if err != nil {
			h.log.Warn("requested confirmation but found no record",
				"chaddr", pkt.HardwareAddr.String(),
				"requested_ip", requestedIP.String(),
			)
			return nil, errNoRecord
		}

		return h.buildAck(pkt, intf, serverAddr, requestedIP, false)
	}

	// case 3: RENEWING/REBINDING.
	h.log.Info("requested renewal",
		"chaddr", pkt.HardwareAddr.String(),
		"ciaddr", pkt.ClientAddr.String(),
	)

	err = h.store.Renew(ctx, pkt.ClientAddr, pkt.HardwareAddr)
	if err != nil {
		h.log.Warn("requested renewal but found no record",
			"chaddr", pkt.HardwareAddr.String(),
			"ciaddr", pkt.ClientAddr.String(),
		)
		return nil, errNoRecord
	}

	return h.buildAck(pkt, intf, serverAddr, pkt.ClientAddr, true)
}

// buildAck constructs a DHCPACK packet.
func (h *Handler) buildAck(pkt *dhcp4.Packet, intf netIface, serverAddr, yourip []byte, includeClientAddr bool) (*dhcp4.Packet, error) {
	opts, err := h.makeOptions()
	if err != nil {
		return nil, err
	}
	opts[dhcp4.OptServerIdentifier] = serverAddr

	resp := &dhcp4.Packet{
		Type:          dhcp4.MsgAck,
		TransactionID: pkt.TransactionID,
		Broadcast:     pkt.Broadcast,
		HardwareAddr:  pkt.HardwareAddr,
		YourAddr:      yourip,
		ServerAddr:    serverAddr,
		RelayAddr:     pkt.RelayAddr,
		Options:       opts,
	}
	if includeClientAddr {
		resp.ClientAddr = pkt.ClientAddr
	}
	return resp, nil
}
