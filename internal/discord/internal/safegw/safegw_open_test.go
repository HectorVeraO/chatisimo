package safegw_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/gateway"
	"github.com/gorilla/websocket"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/safegw"
)

const (
	openTestDeadline = 10 * time.Second
	// shortOpenTimeout is long enough for several retries and short enough to keep the suite fast.
	shortOpenTimeout = 200 * time.Millisecond
	// lateDialWindow outlasts the reconnect delay a live retry loop would still be waiting out.
	lateDialWindow = 600 * time.Millisecond
)

// scriptedGateway is a fake Discord Gateway whose behavior is chosen per connection attempt.
type scriptedGateway struct {
	url         string
	attempts    atomic.Int32
	identifies  atomic.Int32
	active      atomic.Int32
	attemptSeen chan struct{}
	attemptDone chan struct{}

	startsMu sync.Mutex
	starts   []time.Time
}

// startTimes returns when each connection attempt reached the fake, in arrival order.
func (g *scriptedGateway) startTimes() []time.Time {
	g.startsMu.Lock()
	defer g.startsMu.Unlock()

	return append([]time.Time(nil), g.starts...)
}

// attemptFunc handles one connection attempt; attempt counts from 1.
type attemptFunc func(g *scriptedGateway, attempt int, w http.ResponseWriter, r *http.Request)

// failure is one way a connection attempt can go wrong.
type failure func(g *scriptedGateway, w http.ResponseWriter, r *http.Request)

func newScriptedGateway(t *testing.T, script attemptFunc) *scriptedGateway {
	t.Helper()

	g := &scriptedGateway{
		attemptSeen: make(chan struct{}, 256),
		attemptDone: make(chan struct{}, 256),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.startsMu.Lock()
		g.starts = append(g.starts, time.Now())
		g.startsMu.Unlock()
		attempt := int(g.attempts.Add(1))
		g.active.Add(1)
		defer func() {
			g.active.Add(-1)

			select {
			case g.attemptDone <- struct{}{}:
			default:
			}
		}()

		select {
		case g.attemptSeen <- struct{}{}:
		default:
		}

		script(g, attempt, w, r)
	}))
	t.Cleanup(srv.Close)
	g.url = "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	return g
}

// open builds a gateway for the fake and closes it when the test ends.
func (g *scriptedGateway) open(t *testing.T) *safegw.Gateway {
	t.Helper()

	gw := safegw.New(testBotToken, g.url, gateway.IntentGuilds, nil)
	t.Cleanup(func() { gw.Close(context.Background()) })

	return gw
}

// waitAttempts blocks until the fake has seen at least n connection attempts.
func (g *scriptedGateway) waitAttempts(t *testing.T, n int32) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
	defer cancel()

	for g.attempts.Load() < n {
		select {
		case <-g.attemptSeen:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %d connection attempts, saw %d", n, g.attempts.Load())
		}
	}
}

func upgrade(w http.ResponseWriter, r *http.Request) *websocket.Conn {
	upgrader := websocket.Upgrader{}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil
	}

	return ws
}

func sendHello(ws *websocket.Conn) bool {
	hello := map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 60000}}

	return ws.WriteJSON(hello) == nil
}

// awaitIdentify reads the client's next frame and counts it when it is an identify.
func awaitIdentify(g *scriptedGateway, ws *websocket.Conn) bool {
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return false
	}

	var frame struct {
		Op int `json:"op"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		return false
	}
	if frame.Op != int(gateway.OpcodeIdentify) {
		return false
	}
	g.identifies.Add(1)

	return true
}

func sendReady(ws *websocket.Conn) bool {
	ready := map[string]any{
		"op": 0,
		"s":  1,
		"t":  "READY",
		"d": map[string]any{
			"v":          10,
			"session_id": "sess-open-1",
			"user": map[string]any{
				"id":            "100000000000000001",
				"username":      "chatisimo-test",
				"discriminator": "0000",
				"bot":           true,
			},
			"guilds":      []any{},
			"application": map[string]any{"id": "200000000000000001", "flags": 0},
		},
	}

	return ws.WriteJSON(ready) == nil
}

// holdOpen reads until the client closes the socket.
func holdOpen(ws *websocket.Conn) {
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
	}
}

// closeWith sends a close frame with code and drops the socket, as Discord does when it ends a session.
func closeWith(ws *websocket.Conn, code int) {
	frame := websocket.FormatCloseMessage(code, "dropped")
	// Best-effort: the client may already be gone.
	_ = ws.WriteControl(websocket.CloseMessage, frame, time.Now().Add(time.Second))
	// Best-effort teardown of the server side of the socket.
	_ = ws.Close()
}

// serveSession runs a healthy session: hello, identify, READY, then idle until the client leaves.
func serveSession(g *scriptedGateway, w http.ResponseWriter, r *http.Request) {
	ws := upgrade(w, r)
	if ws == nil {
		return
	}
	defer func() {
		// Best-effort close of the server side when the session ends.
		_ = ws.Close()
	}()

	if !sendHello(ws) || !awaitIdentify(g, ws) || !sendReady(ws) {
		return
	}
	holdOpen(ws)
}

func closeAfterIdentify(code int) failure {
	return func(g *scriptedGateway, w http.ResponseWriter, r *http.Request) {
		ws := upgrade(w, r)
		if ws == nil {
			return
		}
		if !sendHello(ws) || !awaitIdentify(g, ws) {
			return
		}
		closeWith(ws, code)
	}
}

func closeAfterHello(code int) failure {
	return func(_ *scriptedGateway, w http.ResponseWriter, r *http.Request) {
		ws := upgrade(w, r)
		if ws == nil {
			return
		}
		if !sendHello(ws) {
			return
		}
		closeWith(ws, code)
	}
}

func closeBeforeHello(code int) failure {
	return func(_ *scriptedGateway, w http.ResponseWriter, r *http.Request) {
		ws := upgrade(w, r)
		if ws == nil {
			return
		}
		closeWith(ws, code)
	}
}

// dropAfterUpgrade ends the TCP connection without a close frame.
func dropAfterUpgrade(_ *scriptedGateway, w http.ResponseWriter, r *http.Request) {
	ws := upgrade(w, r)
	if ws == nil {
		return
	}
	// Best-effort abrupt teardown; the test wants the client to see no close frame.
	_ = ws.Close()
}

func refuseUpgrade(_ *scriptedGateway, w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "unavailable", http.StatusInternalServerError)
}

// firstFrame makes the first frame after the upgrade the given raw text, then idles until the
// client leaves.
func firstFrame(raw string) failure {
	return func(_ *scriptedGateway, w http.ResponseWriter, r *http.Request) {
		ws := upgrade(w, r)
		if ws == nil {
			return
		}
		defer func() {
			// Best-effort close of the server side when the client leaves.
			_ = ws.Close()
		}()

		if ws.WriteMessage(websocket.TextMessage, []byte(raw)) != nil {
			return
		}
		holdOpen(ws)
	}
}

// readyThenDrop runs a session that reaches READY and is then dropped with a resumable code.
func readyThenDrop(g *scriptedGateway, w http.ResponseWriter, r *http.Request) {
	ws := upgrade(w, r)
	if ws == nil {
		return
	}
	if !sendHello(ws) || !awaitIdentify(g, ws) || !sendReady(ws) {
		return
	}
	closeWith(ws, 4000)
}

// failFirst makes the first attempt fail and every later one succeed.
func failFirst(f failure) attemptFunc {
	return func(g *scriptedGateway, attempt int, w http.ResponseWriter, r *http.Request) {
		if attempt == 1 {
			f(g, w, r)
			return
		}
		serveSession(g, w, r)
	}
}

// failAlways makes every attempt fail.
func failAlways(f failure) attemptFunc {
	return func(g *scriptedGateway, _ int, w http.ResponseWriter, r *http.Request) {
		f(g, w, r)
	}
}

func TestOpenRetriesTransientFailuresOfTheFirstConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		fail           failure
		wantIdentifies int32
	}{
		{"a resumable close after the identify", closeAfterIdentify(4000), 2},
		{"a session timeout close after the identify", closeAfterIdentify(4009), 2},
		{"a resumable close before the hello", closeBeforeHello(4000), 1},
		{"a normal closure after the hello", closeAfterHello(websocket.CloseNormalClosure), 1},
		{"a socket dropped right after the upgrade", dropAfterUpgrade, 1},
		{"an HTTP error on the upgrade", refuseUpgrade, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newScriptedGateway(t, failFirst(tc.fail))
			gw := fake.open(t)
			ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
			defer cancel()

			if err := gw.Open(ctx); err != nil {
				t.Fatalf("Open: %v", err)
			}

			if got := fake.attempts.Load(); got != 2 {
				t.Errorf("connection attempts = %d, want 2", got)
			}
			if got := fake.identifies.Load(); got != tc.wantIdentifies {
				t.Errorf("identifies = %d, want %d", got, tc.wantIdentifies)
			}
			if got := gw.Status(); got != gateway.StatusReady {
				t.Errorf("Status() = %v, want ready", got)
			}
		})
	}
}

func TestOpenSucceedsOnAHealthyFirstConnection(t *testing.T) {
	t.Parallel()

	fake := newScriptedGateway(t, func(g *scriptedGateway, _ int, w http.ResponseWriter, r *http.Request) {
		serveSession(g, w, r)
	})
	gw := fake.open(t)
	ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
	defer cancel()

	if err := gw.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if got := fake.attempts.Load(); got != 1 {
		t.Errorf("connection attempts = %d, want 1", got)
	}
	if got := fake.identifies.Load(); got != 1 {
		t.Errorf("identifies = %d, want 1", got)
	}
}

func TestOpenFailsAtOnceOnAFatalClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		fail           failure
		wantIdentifies int32
	}{
		{"authentication failed after the identify", closeAfterIdentify(4004), 1},
		{"authentication failed before the hello", closeBeforeHello(4004), 0},
		{"disallowed intents after the identify", closeAfterIdentify(4014), 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newScriptedGateway(t, failAlways(tc.fail))
			gw := fake.open(t)
			ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
			defer cancel()

			err := gw.Open(ctx)

			if err == nil {
				t.Fatal("Open succeeded, want an error")
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, safegw.ErrOpenTimeout) {
				t.Fatalf("Open = %v, want a prompt failure, not a timeout", err)
			}
			if !strings.Contains(err.Error(), "failed to open gateway connection") {
				t.Errorf("Open error = %q, want the open failure", err)
			}
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) {
				t.Errorf("Open error = %v, want the websocket close error", err)
			}
			if got := fake.attempts.Load(); got != 1 {
				t.Errorf("connection attempts = %d, want 1 (a fatal close is not retried)", got)
			}
			if got := fake.identifies.Load(); got != tc.wantIdentifies {
				t.Errorf("identifies = %d, want %d", got, tc.wantIdentifies)
			}
		})
	}
}

func TestOpenGivesUpAfterTheOpenTimeout(t *testing.T) {
	t.Parallel()

	fake := newScriptedGateway(t, failAlways(closeAfterIdentify(4000)))
	gw := fake.open(t)
	if err := gw.SetOpenTimeout(shortOpenTimeout); err != nil {
		t.Fatalf("SetOpenTimeout: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
	defer cancel()

	start := time.Now()
	err := gw.Open(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Open succeeded, want a timeout error")
	}
	if !errors.Is(err, safegw.ErrOpenTimeout) {
		t.Fatalf("Open error = %v, want ErrOpenTimeout", err)
	}
	if !strings.Contains(err.Error(), "failed to open gateway connection") {
		t.Errorf("Open error = %q, want the open failure", err)
	}
	if elapsed < shortOpenTimeout {
		t.Errorf("Open returned after %s, want at least the %s open timeout", elapsed, shortOpenTimeout)
	}
	if got := fake.attempts.Load(); got < 2 {
		t.Errorf("connection attempts = %d, want the failed first connection retried", got)
	}
	hostPart := strings.TrimPrefix(fake.url, "ws://")
	hostPart, _, _ = strings.Cut(hostPart, "/")
	if strings.Contains(err.Error(), testBotToken) || strings.Contains(err.Error(), hostPart) {
		t.Errorf("Open error = %q, want no token and no server address", err)
	}

	assertNoMoreDials(t, fake)
}

// assertNoMoreDials checks that the gateway stopped dialing once Open returned. A dial already in
// flight when Open returned may still land, so one extra attempt is allowed; a live retry loop
// would make several within the window.
func assertNoMoreDials(t *testing.T, fake *scriptedGateway) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
	defer cancel()

	// An attempt that ends signals attemptDone, so this wait cannot stall on a quiet fake.
	for fake.active.Load() != 0 {
		select {
		case <-fake.attemptDone:
		case <-ctx.Done():
			t.Fatal("timed out waiting for the in-flight connections to end")
		}
	}
	settled := fake.attempts.Load()

	window := time.NewTimer(lateDialWindow)
	defer window.Stop()

	for {
		if got := fake.attempts.Load(); got > settled+1 {
			t.Fatalf("connection attempts grew from %d to %d after Open returned", settled, got)
		}

		select {
		case <-fake.attemptSeen:
		case <-window.C:
			return
		}
	}
}

func TestOpenReturnsWhenTheContextEndsDuringRetries(t *testing.T) {
	t.Parallel()

	fake := newScriptedGateway(t, failAlways(closeAfterIdentify(4000)))
	gw := fake.open(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- gw.Open(ctx)
	}()

	fake.waitAttempts(t, 2)
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Open error = %v, want context.Canceled", err)
		}
	case <-time.After(openTestDeadline):
		t.Fatal("Open did not return after the context was canceled")
	}
}

func TestSetOpenTimeoutRejectsANonPositiveDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gw := safegw.New(testBotToken, "ws://127.0.0.1:1/ws", gateway.IntentGuilds, nil)

			if err := gw.SetOpenTimeout(tc.d); err == nil {
				t.Errorf("SetOpenTimeout(%s) = nil, want an error", tc.d)
			}
		})
	}
}

func TestOpenFailsAtOnceOnAMalformedHandshake(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		first string
	}{
		{"a first frame that is not JSON", "not json"},
		{"a first frame with an opcode other than hello", `{"op":11}`},
		{"a hello whose payload is not an object", `{"op":10,"d":"unexpected"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newScriptedGateway(t, failAlways(firstFrame(tc.first)))
			gw := fake.open(t)
			ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
			defer cancel()

			err := gw.Open(ctx)

			if err == nil {
				t.Fatal("Open succeeded, want an error")
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, safegw.ErrOpenTimeout) {
				t.Fatalf("Open = %v, want a prompt failure, not a timeout", err)
			}
			if !strings.Contains(err.Error(), "failed to open gateway connection") {
				t.Errorf("Open error = %q, want the open failure", err)
			}
			if got := fake.attempts.Load(); got != 1 {
				t.Errorf("connection attempts = %d, want 1 (a malformed handshake is not retried)", got)
			}
			if got := fake.identifies.Load(); got != 0 {
				t.Errorf("identifies = %d, want 0", got)
			}
		})
	}
}

func TestReconnectDelayGrowsAfterConsecutiveFailures(t *testing.T) {
	t.Parallel()

	// A growing delay fits only a handful of attempts in the window; a fixed 20 ms delay would make
	// dozens and hammer the server.
	const (
		window      = 1300 * time.Millisecond
		minAttempts = 4
		maxAttempts = 8
	)

	fake := newScriptedGateway(t, failAlways(closeAfterIdentify(4000)))
	gw := fake.open(t)
	if err := gw.SetOpenTimeout(window); err != nil {
		t.Fatalf("SetOpenTimeout: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
	defer cancel()

	err := gw.Open(ctx)

	if !errors.Is(err, safegw.ErrOpenTimeout) {
		t.Fatalf("Open error = %v, want ErrOpenTimeout", err)
	}
	starts := fake.startTimes()
	if len(starts) < minAttempts {
		t.Fatalf("connection attempts = %d, want at least %d", len(starts), minAttempts)
	}
	if len(starts) > maxAttempts {
		t.Errorf("connection attempts = %d within %s, want at most %d (the delay must grow)", len(starts), window, maxAttempts)
	}
}

func TestReconnectDelayStartsOverAfterAHealthyConnection(t *testing.T) {
	t.Parallel()

	// Five failures grow the delay to 640 ms. Attempt 6 reaches READY and is dropped; if the delay
	// were not reset, attempt 7 would follow no sooner than 640 ms later.
	const (
		healthyAttempt   = 6
		growthFloor      = 20*time.Millisecond + 40*time.Millisecond + 80*time.Millisecond + 160*time.Millisecond + 320*time.Millisecond - 5*time.Millisecond
		resetCeiling     = 500 * time.Millisecond
		unresetDelayFrom = 640 * time.Millisecond
	)

	fake := newScriptedGateway(t, func(g *scriptedGateway, attempt int, w http.ResponseWriter, r *http.Request) {
		if attempt == healthyAttempt {
			readyThenDrop(g, w, r)
			return
		}
		closeAfterIdentify(4000)(g, w, r)
	})
	gw := fake.open(t)
	ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
	defer cancel()

	if err := gw.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	fake.waitAttempts(t, healthyAttempt+1)

	starts := fake.startTimes()
	grown := starts[healthyAttempt-1].Sub(starts[0])
	if grown < growthFloor {
		t.Fatalf("the first %d attempts spanned %s, want at least %s", healthyAttempt, grown, growthFloor)
	}
	afterDrop := starts[healthyAttempt].Sub(starts[healthyAttempt-1])
	if afterDrop >= resetCeiling {
		t.Errorf("gap after the healthy connection = %s, want under %s (a delay of %s would mean no reset)",
			afterDrop, resetCeiling, unresetDelayFrom)
	}
}

func TestCloseDuringTheDialLeavesNoLiveConnection(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDial := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseDial)

	fake := newScriptedGateway(t, func(g *scriptedGateway, _ int, w http.ResponseWriter, r *http.Request) {
		// Hold the upgrade so the client is still dialing when Open gives up.
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}

		ws := upgrade(w, r)
		if ws == nil {
			return
		}
		if !sendHello(ws) || !awaitIdentify(g, ws) {
			return
		}
		closeWith(ws, websocket.CloseNormalClosure)
	})
	gw := fake.open(t)
	if err := gw.SetOpenTimeout(shortOpenTimeout); err != nil {
		t.Fatalf("SetOpenTimeout: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), openTestDeadline)
	defer cancel()

	err := gw.Open(ctx)
	if !errors.Is(err, safegw.ErrOpenTimeout) {
		t.Fatalf("Open error = %v, want ErrOpenTimeout", err)
	}

	releaseDial()
	select {
	case <-fake.attemptDone:
	case <-ctx.Done():
		t.Fatal("timed out waiting for the server side of the socket to end")
	}

	if got := fake.identifies.Load(); got != 0 {
		t.Errorf("identifies = %d, want 0 (a socket dialed after Close must not be used)", got)
	}
	if got := fake.attempts.Load(); got != 1 {
		t.Errorf("connection attempts = %d, want 1", got)
	}
}
