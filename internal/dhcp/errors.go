// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/errors.go).

// Package dhcp implements the DHCPv4 handler and server, adapted from
// sabakan's dhcpd package.
package dhcp

import "errors"

var (
	errNotChosen      = errors.New("not chosen")
	errNoRecord       = errors.New("no record")
	errNoAction       = errors.New("no action required")
	errUnknownMsgType = errors.New("unknown message type")
)
