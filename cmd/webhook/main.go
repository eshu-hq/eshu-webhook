// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Command webhook runs the eshu-webhook satellite: a stateless
// verifier/normalizer that submits provider deliveries to core's versioned
// intake API. It stores nothing; without the intake uplink it refuses to
// start.
package main

import (
	"fmt"
	"net/http"
	"os"

	webhook "github.com/eshu-hq/eshu-webhook"
)

func main() {
	if err := run(os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "webhook:", err)
		os.Exit(1)
	}
}

func run(getenv func(string) string) error {
	cfg, err := webhook.LoadConfig(getenv)
	if err != nil {
		return err
	}
	server, err := webhook.NewServer(cfg, nil)
	if err != nil {
		return err
	}
	return http.ListenAndServe(cfg.ListenAddr, server.Mux())
}
