package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

const testTimeout = 20 * time.Second

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// syncBuffer is a bytes.Buffer safe for a logger writing while the test reads.
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

var listeningLine = regexp.MustCompile(`msg=listening addr=(\S+)`)

// runningFake is a fake started through run.
type runningFake struct {
	addr   string
	stderr *syncBuffer
	cancel context.CancelFunc
	code   chan int
}

func startRun(t *testing.T, args ...string) *runningFake {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	fake := &runningFake{stderr: &syncBuffer{}, cancel: cancel, code: make(chan int, 1)}
	go func() {
		fake.code <- run(ctx, args, io.Discard, fake.stderr)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-fake.code:
		case <-time.After(testTimeout):
			t.Error("run did not return after its context ended")
		}
	})

	deadline := time.NewTimer(testTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for fake.addr == "" {
		match := listeningLine.FindStringSubmatch(fake.stderr.String())
		if match != nil {
			fake.addr = match[1]
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("run never logged its address; stderr:\n%s", fake.stderr.String())
		case <-ticker.C:
		}
	}

	return fake
}

func (f *runningFake) get(t *testing.T, path string, headers map[string]string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+f.addr+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: testTimeout}
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}

func TestRunServesTheRESTAPI(t *testing.T) {
	fake := startRun(t, "--listen", "127.0.0.1:0")

	status := fake.get(t, "/api/v10/gateway", nil)

	if status != http.StatusOK {
		t.Errorf("GET /api/v10/gateway = %d, want 200", status)
	}
	host, _, err := net.SplitHostPort(fake.addr)
	if err != nil || host != "127.0.0.1" {
		t.Errorf("listening address %q, want a 127.0.0.1 port (%v)", fake.addr, err)
	}
	if n := strings.Count(fake.stderr.String(), "msg=listening"); n != 1 {
		t.Errorf("the address was logged %d times, want once", n)
	}
}

func TestRunLogsMethodRouteAndStatusOnly(t *testing.T) {
	fake := startRun(t, "--listen", "127.0.0.1:0")
	secretHeader := map[string]string{"Authorization": "Bot synthetic-header-secret"}

	fake.get(t, "/api/v10/gateway?probe=synthetic-query-secret", secretHeader)
	fake.get(t, "/webhooks/synthetic-path-secret/more", secretHeader)

	logs := fake.stderr.String()
	for _, want := range []string{`method=GET`, `route="GET /api/v10/gateway"`, `status=200`, `status=404`} {
		if !strings.Contains(logs, want) {
			t.Errorf("stderr lacks %s:\n%s", want, logs)
		}
	}
	for _, secret := range []string{"synthetic-header-secret", "synthetic-query-secret", "synthetic-path-secret", "probe="} {
		if strings.Contains(logs, secret) {
			t.Errorf("stderr contains %q:\n%s", secret, logs)
		}
	}
	var requestLines int
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, "msg=request") {
			requestLines++
		}
	}
	if requestLines != 2 {
		t.Errorf("request lines = %d, want 2:\n%s", requestLines, logs)
	}
}

func TestRunStopsGracefullyWhenItsContextEnds(t *testing.T) {
	fake := startRun(t, "--listen", "127.0.0.1:0")
	fake.cancel()

	select {
	case code := <-fake.code:
		if code != exitOK {
			t.Errorf("exit code = %d, want 0; stderr:\n%s", code, fake.stderr.String())
		}
		fake.code <- code
	case <-time.After(testTimeout):
		t.Fatal("run did not return after its context ended")
	}
	if !strings.Contains(fake.stderr.String(), "msg=stopped") {
		t.Errorf("stderr lacks the stop line:\n%s", fake.stderr.String())
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"unknown flag", []string{"--bogus"}, exitUsage},
		{"bad duration", []string{"--heartbeat-interval", "soon"}, exitUsage},
		{"unexpected argument", []string{"extra"}, exitUsage},
		{"help", []string{"-h"}, exitOK},
		{"bad callback deadline", []string{"--callback-deadline", "soon"}, exitUsage},
		{"negative callback deadline", []string{"--listen", "127.0.0.1:0", "--callback-deadline", "-1s"}, exitFailure},
		{"negative heartbeat", []string{"--listen", "127.0.0.1:0", "--heartbeat-interval", "-1s"}, exitFailure},
		{"address that cannot be bound", []string{"--listen", "256.256.256.256:1"}, exitFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr syncBuffer

			code := run(t.Context(), tc.args, io.Discard, &stderr)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, tc.wantCode, stderr.String())
			}
			if tc.wantCode != exitOK && stderr.String() == "" {
				t.Error("nothing was written to stderr")
			}
		})
	}
}

func TestRunServesTheControlAPI(t *testing.T) {
	fake := startRun(t, "--listen", "127.0.0.1:0")

	status := fake.get(t, "/_control/healthz", nil)

	if status != http.StatusOK {
		t.Errorf("GET /_control/healthz = %d, want 200", status)
	}
	if status := fake.get(t, "/_control/healthz?bot=1", nil); status != http.StatusServiceUnavailable {
		t.Errorf("GET /_control/healthz?bot=1 = %d, want 503 while no bot is connected", status)
	}
}

// The control API has no authentication, so the default address must stay on loopback.
func TestDefaultsBindLoopbackAndGiveTheBotThreeSeconds(t *testing.T) {
	var stderr syncBuffer

	code := run(t.Context(), []string{"-h"}, io.Discard, &stderr)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	usage := stderr.String()
	for _, want := range []string{`(default "127.0.0.1:10010")`, `(default 3s)`} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage lacks %q, usage: %s", want, usage)
		}
	}
}
