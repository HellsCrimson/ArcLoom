package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/config"
)

// healthcheckCmd probes this instance's own /healthz and exits 0 when it
// answers. It is the container HEALTHCHECK, and it exists so the runtime image
// need not ship curl just to call itself — a general-purpose fetch tool in an
// image is also a fetch tool for anything that gains execution there.
//
// It deliberately talks to the loopback address rather than to whatever
// ARCLOOM_ADDR names as a host: the probe runs inside the container, and a
// bind address of ":8080" or "0.0.0.0:8080" is not a destination.
func healthcheckCmd() int {
	cfg := config.Load()
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || port == "" {
		port = "8080"
	}
	url := "http://127.0.0.1:" + strings.TrimSpace(port) + "/healthz"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: %s returned %s\n", url, resp.Status)
		return 1
	}
	return 0
}
