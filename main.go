package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/hashicorp/consul/api"
	"go.universe.tf/netboot/dhcp4"
	"golang.org/x/sync/errgroup"

	"github.com/zinrai/fleet-dhcpd/internal/config"
	"github.com/zinrai/fleet-dhcpd/internal/dhcp"
	"github.com/zinrai/fleet-dhcpd/internal/store"
	"github.com/zinrai/fleet-dhcpd/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	kvPrefix := flag.String("kv-prefix", "", "Consul KV prefix containing config and leases (required)")
	ifname := flag.String("interface", "", "network interface to listen on (required)")
	listenAddr := flag.String("listen", "0.0.0.0:67", "address to bind for DHCP")
	showVersion := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	if *showVersion {
		printVersion()
		os.Exit(0)
	}

	if *kvPrefix == "" {
		return fmt.Errorf("-kv-prefix is required")
	}
	if *ifname == "" {
		return fmt.Errorf("-interface is required")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	intf, err := net.InterfaceByName(*ifname)
	if err != nil {
		return fmt.Errorf("interface %q: %w", *ifname, err)
	}
	log.Info("interface bound", "name", intf.Name, "index", intf.Index)

	consulClient, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		return fmt.Errorf("consul client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loader := config.NewLoader(consulClient, *kvPrefix, log)
	holder, initialIndex, err := loader.LoadInitial(ctx)
	if err != nil {
		return fmt.Errorf("initial config load: %w", err)
	}

	cfg := holder.Get()
	log.Info("initial config loaded",
		"subnet", cfg.Subnet.String(),
		"range", fmt.Sprintf("%s-%s", cfg.RangeStart, cfg.RangeEnd),
		"router", cfg.Router.String(),
		"lease_time", cfg.LeaseTime.String(),
		"boot_url", cfg.BootURL,
	)

	// If no webhook URL is configured, pass a nil LeaseObserver so the
	// store skips notification entirely. The Notifier itself would also
	// no-op on an empty URL, but a nil observer makes the absence of
	// the webhook explicit in the type system, matching the LeaseObserver
	// contract documented in internal/store/store.go.
	var observer store.LeaseObserver
	if cfg.WebhookURL != "" {
		observer = webhook.New(cfg.WebhookURL, log)
	}
	consulStore := store.NewConsulStore(consulClient, *kvPrefix, holder, log, observer)
	handler := dhcp.NewHandler(consulStore, holder, log)

	conn, err := dhcp4.NewConn(*listenAddr)
	if err != nil {
		return fmt.Errorf("DHCP listen on %s: %w", *listenAddr, err)
	}
	server := dhcp.NewServer(handler, conn, log)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return server.Serve(gctx)
	})

	g.Go(func() error {
		return loader.Watch(gctx, initialIndex)
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case s := <-sig:
			log.Info("shutting down", "signal", s.String())
			cancel()
		case <-gctx.Done():
		}
	}()

	if err := g.Wait(); err != nil && err != context.Canceled {
		return err
	}
	return nil
}
