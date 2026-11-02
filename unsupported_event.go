// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import "fmt"

// unsupportedEventError reports a provider event or object kind the
// satellite does not normalize. It keeps the historical message text and
// lets the HTTP layer tell an unknown event from a malformed payload.
type unsupportedEventError struct {
	kind  string
	value string
}

func (e *unsupportedEventError) Error() string {
	return fmt.Sprintf("unsupported %s %q", e.kind, e.value)
}

// unsupportedEvent builds the error for an event the normalizer rejects.
func unsupportedEvent(kind, value string) error {
	return &unsupportedEventError{kind: kind, value: value}
}
