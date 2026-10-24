package fakediscord_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// Synthetic fixed IDs shared by the tests.
const (
	aliceID       = "100000000000000001"
	brunoID       = "100000000000000002"
	carolID       = "100000000000000003"
	winterGuildID = "200000000000000001"
	springGuildID = "200000000000000002"
	missingID     = "999999999999999999"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a logger.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("buffer write: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func capturingLogger(buf *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// newFake creates a fake that is closed when the test ends.
func newFake(t testing.TB, opts fakediscord.Options) *fakediscord.Server {
	t.Helper()

	srv, err := fakediscord.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		closeErr := srv.Close()
		if closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})

	return srv
}

// startFake creates a fake listening on a loopback port.
func startFake(t testing.TB, opts fakediscord.Options) *fakediscord.Server {
	t.Helper()

	srv := newFake(t, opts)
	err := srv.Start(t.Context())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	return srv
}

type reply struct {
	status int
	header http.Header
	body   []byte
}

func (r reply) json(t testing.TB) map[string]any {
	t.Helper()

	var decoded map[string]any
	err := json.Unmarshal(r.body, &decoded)
	if err != nil {
		t.Fatalf("response is not a JSON object: %v (status %d, body %q)", err, r.status, r.body)
	}

	return decoded
}

// call sends one request straight to the fake's handler. A non-empty auth is sent as the
// Authorization header.
func call(t testing.TB, srv *fakediscord.Server, method, target, auth, body string) reply {
	t.Helper()

	return callWithHost(t, srv, method, target, auth, body, "")
}

func callWithHost(t testing.TB, srv *fakediscord.Server, method, target, auth, body, host string) reply {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	return reply{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

func botAuth(srv *fakediscord.Server) string {
	return "Bot " + srv.Token()
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for !cond() {
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

// waitChan returns the value sent on ch, failing the test after the timeout.
func waitChan[T any](t testing.TB, ch <-chan T, timeout time.Duration, what string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		var zero T
		t.Fatalf("timed out waiting for %s", what)

		return zero
	}
}

func testContext(t testing.TB, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)

	return ctx
}

func decodeJSON(t testing.TB, data []byte, target any) {
	t.Helper()

	err := json.Unmarshal(data, target)
	if err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
}
