// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"testing"
	"time"
)

// stallingWriter blocks in Write until released, standing in for a slow or
// stuck scrape client on the network response writer.
type stallingWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w *stallingWriter) Write(p []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return len(p), nil
}

// TestMetricsObserveIsNotBlockedByStalledScrape proves a scrape client that
// stops reading cannot hold the metrics lock: observe, which every provider
// request calls, must return while writeText is stuck in Write.
func TestMetricsObserveIsNotBlockedByStalledScrape(t *testing.T) {
	t.Parallel()

	m := newMetrics()
	m.observe("github", outcomeAccepted, reasonAccepted, time.Millisecond)
	w := &stallingWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	scrapeDone := make(chan struct{})
	go func() {
		defer close(scrapeDone)
		m.writeText(w)
	}()
	t.Cleanup(func() {
		close(w.release)
		<-scrapeDone
	})
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scrape never reached Write")
	}

	observed := make(chan struct{})
	go func() {
		m.observe("gitlab", outcomeAccepted, reasonAccepted, time.Millisecond)
		close(observed)
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("observe blocked while a scrape was stalled in Write; the lock is held across the network write")
	}
}
