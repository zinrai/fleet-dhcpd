package main

import "fmt"

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func printVersion() {
	fmt.Printf("fleet-dhcpd %s (commit %s, built %s)\n", version, commit, date)
}
