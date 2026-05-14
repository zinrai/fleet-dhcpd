// Copyright 2018 Cybozu, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// This file is adapted from
// https://github.com/cybozu-go/sabakan (sabakan/dhcpd/server.go).
// Logging is migrated from github.com/cybozu-go/log to log/slog.
// Goroutine lifecycle is migrated from github.com/cybozu-go/well to
// context plus golang.org/x/sync/errgroup.

package dhcp

import (
	"context"
	"log/slog"
	"net"

	"go.universe.tf/netboot/dhcp4"
	"golang.org/x/sync/errgroup"
)

// Server reads DHCP packets and dispatches them to a Handler.
type Server struct {
	handler *Handler
	conn    *dhcp4.Conn
	log     *slog.Logger
}

// NewServer returns a Server that reads from conn and dispatches to handler.
func NewServer(handler *Handler, conn *dhcp4.Conn, log *slog.Logger) *Server {
	return &Server{handler: handler, conn: conn, log: log}
}

// Serve runs until ctx is canceled. The Conn is closed when ctx is canceled.
func (s *Server) Serve(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		<-gctx.Done()
		return s.conn.Close()
	})

	g.Go(func() error {
		return s.recvLoop(gctx)
	})

	err := g.Wait()
	if err == context.Canceled {
		return nil
	}
	return err
}

func (s *Server) recvLoop(ctx context.Context) error {
	for {
		pkt, intf, err := s.conn.RecvDHCP()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.log.Error("RecvDHCP failed", "err", err)
			return err
		}
		if intf == nil {
			s.log.Error("received DHCP packet with no interface information")
			continue
		}

		s.log.Info("received",
			"type", pkt.Type.String(),
			"chaddr", pkt.HardwareAddr.String(),
			"xid", pkt.TransactionID,
		)

		go s.handle(ctx, pkt, intf)
	}
}

func (s *Server) handle(ctx context.Context, pkt *dhcp4.Packet, intf *net.Interface) {
	resp, err := s.handler.ServeDHCP(ctx, pkt, nativeInterface{intf})
	switch err {
	case errNotChosen, errNoRecord, errNoAction, errUnknownMsgType:
		return
	case nil:
		// continue to send
	default:
		s.log.Error("handler failed", "err", err)
		return
	}

	if resp == nil {
		return
	}

	s.log.Info("sending",
		"type", resp.Type.String(),
		"chaddr", resp.HardwareAddr.String(),
		"yiaddr", resp.YourAddr.String(),
	)

	if err := s.conn.SendDHCP(resp, intf); err != nil {
		s.log.Error("SendDHCP failed", "err", err)
	}
}
