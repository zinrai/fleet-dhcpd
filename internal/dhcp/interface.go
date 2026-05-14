// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/interface.go).

package dhcp

import "net"

// netIface is an abstract network interface used by DHCP handlers,
// allowing tests to substitute a fake interface.
type netIface interface {
	Addrs() ([]net.Addr, error)
	Name() string
}

type nativeInterface struct {
	*net.Interface
}

func (i nativeInterface) Name() string {
	return i.Interface.Name
}
