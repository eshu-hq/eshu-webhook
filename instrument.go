// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// WithLogger sets the logger that receives one structured record per
// provider request and returns the server. The default discards logs so
// library use and tests stay quiet; cmd/webhook installs a JSON logger.
// Call it before serving starts: it writes a field without synchronization
// and is not safe while requests are in flight.
func (s *Server) WithLogger(logger *slog.Logger) *Server {
	if logger != nil {
		s.logger = logger
	}
	return s
}

// discardLogger is the default: a logger whose handler drops every record.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// deliveryHeaders names the provider header that carries a delivery id.
// Providers absent here (PagerDuty, Jira, AWS, GCP) log the content-hash
// fallback the intake submission also uses.
var deliveryHeaders = map[string]string{
	"github":    "X-GitHub-Delivery",
	"gitlab":    "X-Gitlab-Delivery",
	"bitbucket": "X-Request-UUID",
}

// tokenProviders authenticate with a static token, not an HMAC signature, so
// a 401 from them is reported as bad_token.
var tokenProviders = map[string]bool{"gitlab": true, "aws": true, "gcp": true}

// maxLoggedDeliveryID bounds an attacker-supplied delivery header before it
// reaches a log line.
const maxLoggedDeliveryID = 200

// requestRecorder wraps the response writer to capture what the request
// telemetry needs: the status the handler wrote, an explicit machine reason
// when the handler knows one, and the body reference for the delivery id
// fallback. It stores only a slice header for the body and never logs it.
// It embeds http.ResponseWriter only, so it hides http.Flusher and Unwrap
// from handlers; the webhook handlers write one small JSON body and need
// neither.
type requestRecorder struct {
	http.ResponseWriter
	status  int
	reason  string
	ignored bool
	body    []byte
}

func (r *requestRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *requestRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}

// setReason records a handler-known machine reason for the request. It is a
// no-op when the writer is not instrumented.
func setReason(w http.ResponseWriter, reason string) {
	if rec, ok := w.(*requestRecorder); ok {
		rec.reason = reason
	}
}

// setIgnored marks a verified delivery the satellite answered itself, without
// an intake call, under the given machine reason. It is a no-op when the
// writer is not instrumented.
func setIgnored(w http.ResponseWriter, reason string) {
	if rec, ok := w.(*requestRecorder); ok {
		rec.reason = reason
		rec.ignored = true
	}
}

// noteBody keeps a reference to the verified-size body so the delivery id
// fallback can hash it at log time.
func noteBody(w http.ResponseWriter, body []byte) {
	if rec, ok := w.(*requestRecorder); ok {
		rec.body = body
	}
}

// instrument wraps one provider handler with request telemetry: it times
// the call, then emits one structured log record and updates the
// requests_total and request_duration_seconds series. A panicking handler is
// counted as failed/panic and the panic is re-raised for net/http.
func (s *Server) instrument(provider string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &requestRecorder{ResponseWriter: w}
		defer func() {
			panicked := recover()
			status := rec.status
			if panicked != nil {
				status = http.StatusInternalServerError
				rec.reason = reasonPanic
			}
			if status == 0 {
				status = http.StatusOK
			}
			outcome, reason := classify(provider, status, rec.reason)
			if rec.ignored && status < 400 {
				outcome = outcomeIgnored
			}
			elapsed := time.Since(start)
			s.metrics.observe(provider, outcome, reason, elapsed)
			s.logRequest(r, provider, status, outcome, reason, rec.body, elapsed)
			if panicked != nil {
				panic(panicked)
			}
		}()
		next(rec, r)
	}
}

// classify maps a response to the closed outcome and reason vocabulary. A
// handler-set reason wins; otherwise the reason follows from the status.
func classify(provider string, status int, explicit string) (outcome, reason string) {
	switch {
	case status >= 500:
		outcome = outcomeFailed
	case status >= 400:
		outcome = outcomeRejected
	default:
		outcome = outcomeAccepted
	}
	if explicit != "" {
		if explicit == reasonPanic {
			return outcomeFailed, explicit
		}
		return outcome, explicit
	}
	switch status {
	case http.StatusAccepted:
		return outcome, reasonAccepted
	case http.StatusBadRequest:
		return outcome, reasonUnreadableBody
	case http.StatusUnauthorized:
		if tokenProviders[provider] {
			return outcome, reasonBadToken
		}
		return outcome, reasonBadSignature
	case http.StatusMethodNotAllowed:
		return outcome, reasonMethodNotAllowed
	case http.StatusConflict:
		return outcome, reasonDeliveryConflict
	case http.StatusRequestEntityTooLarge:
		return outcome, reasonOversizedBody
	case http.StatusUnprocessableEntity:
		return outcome, reasonInvalidPayload
	case http.StatusBadGateway:
		return outcome, reasonIntakeFailed
	default:
		return outcome, reasonOther
	}
}

// logRequest writes the per-request record: route, provider, status, outcome,
// machine reason, delivery id, and duration. It carries no body, header
// value other than the delivery id, or credential.
func (s *Server) logRequest(r *http.Request, provider string, status int, outcome, reason string, body []byte, elapsed time.Duration) {
	level := slog.LevelInfo
	switch outcome {
	case outcomeRejected:
		level = slog.LevelWarn
	case outcomeFailed:
		level = slog.LevelError
	}
	s.logger.LogAttrs(r.Context(), level, "webhook request",
		slog.String("route", r.URL.Path),
		slog.String("provider", provider),
		slog.String("method", r.Method),
		slog.Int("status", status),
		slog.String("outcome", outcome),
		slog.String("reason", reason),
		slog.String("delivery_id", loggedDeliveryID(provider, r, body)),
		slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
	)
}

// loggedDeliveryID returns the provider delivery id, or the content-hash
// fallback when the body was read, or "" when no body was read at all.
func loggedDeliveryID(provider string, r *http.Request, body []byte) string {
	header := ""
	if name := deliveryHeaders[provider]; name != "" {
		header = r.Header.Get(name)
	}
	if header == "" && body == nil {
		return ""
	}
	id := deliveryID(header, body)
	if len(id) > maxLoggedDeliveryID {
		id = id[:maxLoggedDeliveryID]
	}
	return id
}

// handleMetrics serves the Prometheus text exposition. It is read-only.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method must be GET")
		return
	}
	w.Header().Set("Content-Type", metricsContentType)
	if r.Method == http.MethodHead {
		return
	}
	s.metrics.writeText(w)
}

// writeUnprocessable answers 422 and tags the request with unsupported_event
// when the normalizer rejected the event type, invalid_payload otherwise.
func writeUnprocessable(w http.ResponseWriter, err error) {
	reason := reasonInvalidPayload
	var unsupported *unsupportedEventError
	if errors.As(err, &unsupported) {
		reason = ReasonUnsupportedEvent
	}
	setReason(w, reason)
	writeError(w, http.StatusUnprocessableEntity, err.Error())
}
