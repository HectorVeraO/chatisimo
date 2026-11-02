// Package safegw provides a concurrency-safe Discord gateway client that satisfies
// gateway.Gateway and runs cleanly under the Go race detector.
package safegw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/gateway"
	"github.com/gorilla/websocket"
)

// Gateway is a race-free implementation of gateway.Gateway. It synchronizes all state
// transitions, heartbeats, and socket writes under mutexes to satisfy the Go race detector (-race).
type Gateway struct {
	token   string
	url     string
	intents gateway.Intents

	handlerMu    sync.RWMutex
	eventHandler gateway.EventHandlerFunc
	closeHandler gateway.CloseHandlerFunc

	connMu  sync.Mutex
	conn    *websocket.Conn
	closeMu sync.Mutex
	closed  bool

	statusMu sync.RWMutex
	status   gateway.Status

	seqMu   sync.RWMutex
	lastSeq int

	sessionMu sync.RWMutex
	sessionID string
	resumeURL string

	heartbeatMu       sync.Mutex
	heartbeatCancel   context.CancelFunc
	heartbeatInterval time.Duration

	heartbeatSentMu   sync.Mutex
	lastHeartbeatSent time.Time

	latencyMu sync.RWMutex
	latency   time.Duration

	done chan struct{}
}

var _ gateway.Gateway = (*Gateway)(nil)

// New constructs a new safe Gateway ready to connect.
func New(token, url string, intents gateway.Intents, closeHandler gateway.CloseHandlerFunc) *Gateway {
	return &Gateway{
		token:        token,
		url:          url,
		intents:      intents,
		closeHandler: closeHandler,
		status:       gateway.StatusUnconnected,
		done:         make(chan struct{}),
	}
}

// SetEventHandlerFunc sets the handler for incoming dispatch events.
func (g *Gateway) SetEventHandlerFunc(h gateway.EventHandlerFunc) {
	g.handlerMu.Lock()
	defer g.handlerMu.Unlock()
	g.eventHandler = h
}

// ShardID returns 0 as Chatisimo operates as a single shard.
func (g *Gateway) ShardID() int {
	return 0
}

// ShardCount returns 1 as Chatisimo operates as a single shard.
func (g *Gateway) ShardCount() int {
	return 1
}

// SessionID returns the current Discord gateway session ID if connected.
func (g *Gateway) SessionID() *string {
	g.sessionMu.RLock()
	defer g.sessionMu.RUnlock()
	if g.sessionID == "" {
		return nil
	}
	s := g.sessionID
	return &s
}

// LastSequenceReceived returns the latest sequence number received from Discord.
func (g *Gateway) LastSequenceReceived() *int {
	g.seqMu.RLock()
	defer g.seqMu.RUnlock()
	if g.lastSeq == 0 {
		return nil
	}
	s := g.lastSeq
	return &s
}

// ResumeURL returns the gateway resume URL provided by Discord in the Ready event.
func (g *Gateway) ResumeURL() *string {
	g.sessionMu.RLock()
	defer g.sessionMu.RUnlock()
	if g.resumeURL == "" {
		return nil
	}
	s := g.resumeURL
	return &s
}

// Intents returns the configured gateway intents.
func (g *Gateway) Intents() gateway.Intents {
	return g.intents
}

// Status returns the current gateway connection status.
func (g *Gateway) Status() gateway.Status {
	g.statusMu.RLock()
	defer g.statusMu.RUnlock()
	return g.status
}

// Latency returns the round-trip latency measured between heartbeat and heartbeat ACK.
func (g *Gateway) Latency() time.Duration {
	g.latencyMu.RLock()
	defer g.latencyMu.RUnlock()
	return g.latency
}

// Presence returns nil as custom presence is not configured.
func (g *Gateway) Presence() *gateway.MessageDataPresenceUpdate {
	return nil
}

// Open connects the gateway to Discord and waits until Ready or Resumed is received.
func (g *Gateway) Open(ctx context.Context) error {
	readyChan := make(chan error, 1)
	var readyOnce sync.Once
	ready := func(err error) {
		readyOnce.Do(func() {
			readyChan <- err
		})
	}

	//nolint:gosec // gateway lifecycle goroutine outlives the Open connection deadline context.
	go g.lifecycle(ready)

	select {
	case <-ctx.Done():
		g.Close(ctx)
		return fmt.Errorf("safegw: %w", ctx.Err())
	case err := <-readyChan:
		if err != nil {
			g.Close(ctx)
			return fmt.Errorf("failed to open gateway connection: %w", err)
		}
	}

	return nil
}

func (g *Gateway) lifecycle(ready func(error)) {
	// Ensure ready is called before goroutine exits if an unexpected condition occurs.
	defer ready(nil)

	for {
		g.closeMu.Lock()
		closed := g.closed
		g.closeMu.Unlock()
		if closed {
			return
		}

		err := g.connectAndRead(ready)

		g.closeMu.Lock()
		closed = g.closed
		g.closeMu.Unlock()
		if closed {
			return
		}

		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				closeCode := gateway.CloseEventCodeByCode(closeErr.Code)
				if !closeCode.Reconnect {
					g.CloseWithCode(context.Background(), closeErr.Code, closeErr.Text)
					g.handlerMu.RLock()
					ch := g.closeHandler
					g.handlerMu.RUnlock()
					if ch != nil {
						ch(g, err, false)
					}
					return
				}
			}
		}

		// Brief pause before reconnecting.
		select {
		case <-g.done:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (g *Gateway) connectAndRead(ready func(error)) error {
	g.sessionMu.RLock()
	resumeURL := g.resumeURL
	sessID := g.sessionID
	g.sessionMu.RUnlock()

	dialURL := g.url
	if IsValidRedirectURL(resumeURL, g.url) {
		dialURL = resumeURL
	}

	g.statusMu.Lock()
	g.status = gateway.StatusConnecting
	g.statusMu.Unlock()

	dialer := websocket.DefaultDialer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, resp, err := dialer.DialContext(ctx, dialURL, nil)
	cancel()
	if resp != nil && resp.Body != nil {
		// Close HTTP response body to release network resources.
		_ = resp.Body.Close()
	}
	if err != nil {
		ready(err)
		return fmt.Errorf("safegw: dial: %w", err)
	}

	g.connMu.Lock()
	g.conn = conn
	g.connMu.Unlock()

	// 1. Read OpcodeHello.
	_, helloData, err := conn.ReadMessage()
	if err != nil {
		ready(err)
		// Close connection immediately on read failure.
		_ = conn.Close()
		return fmt.Errorf("safegw: read hello: %w", err)
	}

	var helloMsg gateway.Message
	if err := json.Unmarshal(helloData, &helloMsg); err != nil {
		ready(err)
		// Close connection on malformed hello payload.
		_ = conn.Close()
		return fmt.Errorf("safegw: unmarshal hello: %w", err)
	}
	if helloMsg.Op != gateway.OpcodeHello {
		err := fmt.Errorf("safegw: expected OpcodeHello, got %d", helloMsg.Op)
		ready(err)
		// Close connection on unexpected initial opcode.
		_ = conn.Close()
		return err
	}

	hello, ok := helloMsg.D.(gateway.MessageDataHello)
	if !ok {
		err := errors.New("safegw: invalid hello payload")
		ready(err)
		// Close connection on invalid hello structure.
		_ = conn.Close()
		return err
	}

	interval := time.Duration(hello.HeartbeatInterval) * time.Millisecond
	hCtx, hCancel := context.WithCancel(context.Background())
	g.heartbeatMu.Lock()
	g.heartbeatCancel = hCancel
	g.heartbeatInterval = interval
	g.heartbeatMu.Unlock()

	go g.heartbeatLoop(hCtx, interval)

	// 2. Identify or Resume.
	g.seqMu.RLock()
	seq := g.lastSeq
	g.seqMu.RUnlock()

	if sessID != "" && seq > 0 {
		resumeData := gateway.MessageDataResume{
			Token:     g.token,
			SessionID: sessID,
			Seq:       seq,
		}
		if err := g.Send(context.Background(), gateway.OpcodeResume, resumeData); err != nil {
			g.stopHeartbeat()
			// Close socket on resume transmission failure.
			_ = conn.Close()
			return fmt.Errorf("safegw: send resume: %w", err)
		}
	} else {
		identifyData := gateway.MessageDataIdentify{
			Token:   g.token,
			Intents: g.intents,
			Properties: gateway.IdentifyCommandDataProperties{
				OS:      runtime.GOOS,
				Browser: "Chatisimo",
				Device:  "Chatisimo",
			},
		}
		if err := g.Send(context.Background(), gateway.OpcodeIdentify, identifyData); err != nil {
			ready(err)
			g.stopHeartbeat()
			// Close socket on identify transmission failure.
			_ = conn.Close()
			return fmt.Errorf("safegw: send identify: %w", err)
		}
	}

	// 3. Read loop until error or reconnect.
	defer func() {
		g.stopHeartbeat()
		g.connMu.Lock()
		if g.conn == conn {
			// Clean up socket when reader exits.
			_ = g.conn.Close()
			g.conn = nil
		}
		g.connMu.Unlock()
	}()

	return g.readLoop(conn, ready)
}

func (g *Gateway) readLoop(conn *websocket.Conn, ready func(error)) error {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			g.statusMu.RLock()
			status := g.status
			g.statusMu.RUnlock()
			if status != gateway.StatusReady {
				ready(err)
			}
			return fmt.Errorf("safegw: read message: %w", err)
		}

		var msg gateway.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if shouldBreak, loopErr := g.handleMessage(&msg, ready); shouldBreak {
			return loopErr
		}
	}
}

func (g *Gateway) handleMessage(msg *gateway.Message, ready func(error)) (bool, error) {
	switch msg.Op {
	case gateway.OpcodeDispatch:
		g.handleDispatch(msg, ready)

	case gateway.OpcodeHeartbeat:
		g.sendHeartbeat()

	case gateway.OpcodeHeartbeatACK:
		g.heartbeatSentMu.Lock()
		sent := g.lastHeartbeatSent
		g.heartbeatSentMu.Unlock()
		if !sent.IsZero() {
			g.latencyMu.Lock()
			g.latency = time.Since(sent)
			g.latencyMu.Unlock()
		}

	case gateway.OpcodeReconnect:
		return true, errors.New("safegw: server requested reconnect")

	case gateway.OpcodeInvalidSession:
		g.sessionMu.Lock()
		g.sessionID = ""
		g.resumeURL = ""
		g.sessionMu.Unlock()
		return true, errors.New("safegw: invalid session")
	}

	return false, nil
}

func (g *Gateway) handleDispatch(msg *gateway.Message, ready func(error)) {
	g.seqMu.Lock()
	g.lastSeq = msg.S
	g.seqMu.Unlock()

	eventData, _ := msg.D.(gateway.EventData)
	if readyEvent, ok := eventData.(gateway.EventReady); ok {
		var resumeURL string
		candidate := readyEvent.ResumeGatewayURL
		if IsValidRedirectURL(candidate, g.url) {
			resumeURL = candidate
		}
		g.sessionMu.Lock()
		g.sessionID = readyEvent.SessionID
		g.resumeURL = resumeURL
		g.sessionMu.Unlock()

		g.statusMu.Lock()
		g.status = gateway.StatusReady
		g.statusMu.Unlock()

		ready(nil)
	} else if _, ok := eventData.(gateway.EventResumed); ok {
		g.statusMu.Lock()
		g.status = gateway.StatusReady
		g.statusMu.Unlock()

		ready(nil)
	}

	g.handlerMu.RLock()
	handler := g.eventHandler
	g.handlerMu.RUnlock()
	if handler != nil {
		handler(g, msg.T, msg.S, eventData)
	}
}

// Send sends an opcode and payload across the gateway websocket connection.
func (g *Gateway) Send(_ context.Context, op gateway.Opcode, data gateway.MessageData) error {
	payload, err := json.Marshal(gateway.Message{
		Op: op,
		D:  data,
	})
	if err != nil {
		return fmt.Errorf("safegw: marshal message: %w", err)
	}

	g.connMu.Lock()
	defer g.connMu.Unlock()

	if g.conn == nil {
		return errors.New("safegw: gateway not connected")
	}

	if err := g.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("safegw: write message: %w", err)
	}

	return nil
}

// Close gracefully terminates the gateway connection.
func (g *Gateway) Close(ctx context.Context) {
	g.CloseWithCode(ctx, websocket.CloseNormalClosure, "Shutting down")
}

// CloseWithCode terminates the gateway connection with the specified close code.
func (g *Gateway) CloseWithCode(_ context.Context, code int, message string) {
	g.closeMu.Lock()
	if g.closed {
		g.closeMu.Unlock()
		return
	}
	g.closed = true
	close(g.done)
	g.closeMu.Unlock()

	g.stopHeartbeat()

	g.connMu.Lock()
	if g.conn != nil {
		closeMsg := websocket.FormatCloseMessage(code, message)
		// Best-effort write of the close frame before tearing down the socket.
		_ = g.conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
		// Close underlying connection regardless of control write outcome.
		_ = g.conn.Close()
		g.conn = nil
	}
	g.connMu.Unlock()

	g.statusMu.Lock()
	g.status = gateway.StatusDisconnected
	g.statusMu.Unlock()
}

func (g *Gateway) stopHeartbeat() {
	g.heartbeatMu.Lock()
	if g.heartbeatCancel != nil {
		g.heartbeatCancel()
		g.heartbeatCancel = nil
	}
	g.heartbeatMu.Unlock()
}

func (g *Gateway) sendHeartbeat() {
	g.seqMu.RLock()
	seq := g.lastSeq
	g.seqMu.RUnlock()

	g.heartbeatSentMu.Lock()
	g.lastHeartbeatSent = time.Now()
	g.heartbeatSentMu.Unlock()

	// Heartbeats are best-effort; failure triggers reconnection on read timeout.
	_ = g.Send(context.Background(), gateway.OpcodeHeartbeat, gateway.MessageDataHeartbeat(seq))
}

func (g *Gateway) heartbeatLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.sendHeartbeat()
		}
	}
}

// IsValidRedirectURL reports whether rawURL is an allowed Gateway resume URL for a client
// configured with baseURL. It permits only ws or wss URLs with no userinfo, fragment, or
// backslashes whose scheme and host:port match baseURL, or wss URLs on discord.gg subdomains
// when baseURL is a wss Discord Gateway URL.
func IsValidRedirectURL(rawURL, baseURL string) bool {
	if rawURL == "" || baseURL == "" {
		return false
	}
	if strings.ContainsAny(rawURL, "\\ \t\r\n") || strings.ContainsAny(baseURL, "\\ \t\r\n") {
		return false
	}

	target, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return false
	}

	if !isCleanWebSocketURL(target) || !isCleanWebSocketURL(base) {
		return false
	}

	sameScheme := strings.EqualFold(target.Scheme, base.Scheme)
	if !sameScheme {
		return false
	}

	sameHost := strings.EqualFold(target.Host, base.Host)
	if sameHost {
		return true
	}

	isSecureDiscordBase := strings.EqualFold(base.Scheme, "wss") && isDiscordGatewayHost(base.Hostname())
	if !isSecureDiscordBase {
		return false
	}

	targetPort := target.Port()
	hasAllowedPort := targetPort == "" || targetPort == "443"
	isAllowedDiscordTarget := hasAllowedPort && isDiscordGatewayHost(target.Hostname())
	return isAllowedDiscordTarget
}

func isCleanWebSocketURL(u *url.URL) bool {
	if u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Host == "" || u.Hostname() == "" {
		return false
	}

	isWebSocketScheme := strings.EqualFold(u.Scheme, "ws") || strings.EqualFold(u.Scheme, "wss")
	return isWebSocketScheme
}

func isDiscordGatewayHost(host string) bool {
	lower := strings.ToLower(host)
	if lower == "discord.gg" {
		return true
	}

	sub, hasSuffix := strings.CutSuffix(lower, ".discord.gg")
	if !hasSuffix || sub == "" {
		return false
	}

	return isValidSubdomainLabel(sub)
}

func isValidSubdomainLabel(sub string) bool {
	for _, label := range strings.Split(sub, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			isAlphaNum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
			if !isAlphaNum && ch != '-' {
				return false
			}
		}
	}

	return true
}
