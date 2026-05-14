// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/decline.go).

package dhcp

import (
	"context"

	"go.universe.tf/netboot/dhcp4"
)

func (h *Handler) handleDecline(ctx context.Context, pkt *dhcp4.Packet, intf netIface) (*dhcp4.Packet, error) {
	serverAddr, err := getIPv4AddrForInterface(intf)
	if err != nil {
		return nil, err
	}

	serverIdentifier, err := pkt.Options.IP(dhcp4.OptServerIdentifier)
	if err != nil {
		return nil, err
	}

	if !serverAddr.Equal(serverIdentifier) {
		h.log.Info("ignored decline to another server",
			"chaddr", pkt.HardwareAddr.String(),
			"server_identifier", serverIdentifier.String(),
		)
		return nil, errNotChosen
	}

	requestedIP, err := pkt.Options.IP(dhcp4.OptRequestedIP)
	if err != nil {
		return nil, err
	}

	if err := h.store.Decline(ctx, requestedIP, pkt.HardwareAddr); err != nil {
		return nil, err
	}

	h.log.Info("marked address as declined",
		"chaddr", pkt.HardwareAddr.String(),
		"requested_ip", requestedIP.String(),
	)
	return nil, errNoAction
}
