// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

// Package webhook is the eshu-webhook satellite: a stateless
// verifier/normalizer for provider webhook deliveries.
//
// The satellite verifies provider signatures, normalizes deliveries into the
// versioned core intake API (POST /api/v1/webhook/intake/*, bearer auth,
// 1 MiB bounds), and submits them. It performs zero storage: migrations and
// durable trigger truth stay core-side (eshu-hq/eshu#9377). Refs #8876.
package webhook
