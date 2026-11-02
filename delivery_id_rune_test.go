// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestLoggedDeliveryIDCutsOnARuneBoundary proves truncating an over-long
// delivery header never splits a multi-byte rune. The header puts a 3-byte
// rune across byte 200, so a byte slice at 200 would leave invalid UTF-8.
func TestLoggedDeliveryIDCutsOnARuneBoundary(t *testing.T) {
	t.Parallel()

	// 199 ASCII bytes, then a 3-byte rune that spans bytes 199..201.
	header := strings.Repeat("a", maxLoggedDeliveryID-1) + "€" + strings.Repeat("b", 50)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-GitHub-Delivery", header)

	id := loggedDeliveryID("github", req, []byte("{}"))
	if len(id) > maxLoggedDeliveryID {
		t.Fatalf("logged delivery id is %d bytes, want <= %d", len(id), maxLoggedDeliveryID)
	}
	if !utf8.ValidString(id) {
		t.Fatalf("logged delivery id is not valid UTF-8: %q", id)
	}
	if want := strings.Repeat("a", maxLoggedDeliveryID-1); id != want {
		t.Fatalf("logged delivery id = %q, want the %d bytes before the straddling rune", id, len(want))
	}
}

// TestLoggedDeliveryIDKeepsAWholeRuneAtTheBound keeps the full bound when the
// last rune ends exactly on it.
func TestLoggedDeliveryIDKeepsAWholeRuneAtTheBound(t *testing.T) {
	t.Parallel()

	header := strings.Repeat("a", maxLoggedDeliveryID-3) + "€" + "tail"
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-GitHub-Delivery", header)

	id := loggedDeliveryID("github", req, []byte("{}"))
	if len(id) != maxLoggedDeliveryID || !utf8.ValidString(id) {
		t.Fatalf("logged delivery id = %d bytes valid=%v, want %d valid bytes", len(id), utf8.ValidString(id), maxLoggedDeliveryID)
	}
}

// longestValidPrefix is the test's reference for truncateOnRune on valid
// input: the longest prefix of s at or below limit bytes that is valid UTF-8.
func longestValidPrefix(s string, limit int) string {
	n := limit
	if n > len(s) {
		n = len(s)
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}

// TestTruncateOnRuneEveryStraddlePosition places a 2-, 3-, and 4-byte rune at
// every start offset from 197 through 201 of a 200-byte cut. That covers a cut
// landing on each continuation byte of each width (up to three bytes back), so
// a look-back bound that is too short leaves invalid UTF-8 and fails.
func TestTruncateOnRuneEveryStraddlePosition(t *testing.T) {
	t.Parallel()

	const limit = 200
	runes := map[string]string{"2-byte": "é", "3-byte": "€", "4-byte": "\U0001F600"}
	for name, r := range runes {
		for start := 197; start <= 201; start++ {
			in := strings.Repeat("a", start) + r + strings.Repeat("b", 50)
			got := truncateOnRune(in, limit)
			if len(got) > limit {
				t.Errorf("%s at %d: %d bytes, want <= %d", name, start, len(got), limit)
			}
			if !utf8.ValidString(got) {
				t.Errorf("%s at %d: invalid UTF-8 %q", name, start, got[len(got)-4:])
			}
			if want := longestValidPrefix(in, limit); got != want {
				t.Errorf("%s at %d: got %d bytes, want the longest valid prefix of %d bytes", name, start, len(got), len(want))
			}
		}
	}
}

// TestTruncateOnRuneInvalidInput keeps the bound for raw non-UTF-8 bytes: a
// long run of continuation bytes has no rune start to back up to, so the cut
// stops after the bounded look-back and the result is still a prefix within
// the limit.
func TestTruncateOnRuneInvalidInput(t *testing.T) {
	t.Parallel()

	in := strings.Repeat("\x80", 300)
	got := truncateOnRune(in, 200)
	if len(got) > 200 {
		t.Fatalf("got %d bytes, want <= 200", len(got))
	}
	if !strings.HasPrefix(in, got) {
		t.Fatalf("output is not a prefix of the input")
	}
}

// TestTruncateOnRuneShortInputs leaves empty and under-bound strings alone.
func TestTruncateOnRuneShortInputs(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "abc", strings.Repeat("a", 200), strings.Repeat("€", 66)} {
		if got := truncateOnRune(in, 200); got != in {
			t.Errorf("truncateOnRune(%q...) = %d bytes, want unchanged %d", in[:min(len(in), 3)], len(got), len(in))
		}
	}
	if got := truncateOnRune("", 0); got != "" {
		t.Errorf("truncateOnRune(\"\", 0) = %q, want empty", got)
	}
}
