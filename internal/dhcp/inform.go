// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/inform.go).

package dhcp

import (
	"context"

	"go.universe.tf/netboot/dhcp4"
)

func (h *Handler) handleInform(ctx context.Context, pkt *dhcp4.Packet, intf netIface) (*dhcp4.Packet, error) {
	serverAddr, err := getIPv4AddrForInterface(intf)
	if err != nil {
		return nil, err
	}

	opts, err := h.makeOptions()
	if err != nil {
		return nil, err
	}
	delete(opts, dhcp4.OptLeaseTime)
	opts[dhcp4.OptServerIdentifier] = serverAddr

	resp := &dhcp4.Packet{
		Type:          dhcp4.MsgAck,
		TransactionID: pkt.TransactionID,
		Broadcast:     pkt.Broadcast,
		HardwareAddr:  pkt.HardwareAddr,
		ClientAddr:    pkt.ClientAddr,
		ServerAddr:    serverAddr,
		RelayAddr:     pkt.RelayAddr,
		Options:       opts,
	}
	return resp, nil
}
