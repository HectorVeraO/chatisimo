package fakediscord_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/disgoorg/disgo/gateway"
	"github.com/gorilla/websocket"
)

// safeGateway is a race-free implementation of gateway.Gateway for testing against fakediscord.
// It synchronizes all state transitions, heartbeats, and socket writes under mutexes to satisfy
// the Go race detector (-race).
type safeGateway struct {
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

var _ gateway.Gateway = (*safeGateway)(nil)

func newSafeGateway(token, url string, intents gateway.Intents, closeHandler gateway.CloseHandlerFunc) *safeGateway {
	return &safeGateway{
		token:        token,
		url:          url,
		intents:      intents,
		closeHandler: closeHandler,
		status:       gateway.StatusUnconnected,
		done:         make(chan struct{}),
	}
}

func (g *safeGateway) SetEventHandlerFunc(h gateway.EventHandlerFunc) {
	g.handlerMu.Lock()
	defer g.handlerMu.Unlock()
	g.eventHandler = h
}

func (g *safeGateway) ShardID() int {
	return 0
}

func (g *safeGateway) ShardCount() int {
	return 1
}

func (g *safeGateway) SessionID() *string {
	g.sessionMu.RLock()
	defer g.sessionMu.RUnlock()
	if g.sessionID == "" {
		return nil
	}
	s := g.sessionID
	return &s
}

func (g *safeGateway) LastSequenceReceived() *int {
	g.seqMu.RLock()
	defer g.seqMu.RUnlock()
	if g.lastSeq == 0 {
		return nil
	}
	s := g.lastSeq
	return &s
}

func (g *safeGateway) ResumeURL() *string {
	g.sessionMu.RLock()
	defer g.sessionMu.RUnlock()
	if g.resumeURL == "" {
		return nil
	}
	u := g.resumeURL
	return &u
}

func (g *safeGateway) Intents() gateway.Intents {
	return g.intents
}

func (g *safeGateway) Status() gateway.Status {
	g.statusMu.RLock()
	defer g.statusMu.RUnlock()
	return g.status
}

func (g *safeGateway) Latency() time.Duration {
	g.latencyMu.RLock()
	defer g.latencyMu.RUnlock()
	return g.latency
}

func (g *safeGateway) Presence() *gateway.MessageDataPresenceUpdate {
	return nil
}

func (g *safeGateway) Open(ctx context.Context) error {
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

func (g *safeGateway) lifecycle(ready func(error)) {
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

		// Brief pause before reconnecting
		select {
		case <-g.done:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (g *safeGateway) connectAndRead(ready func(error)) error {
	g.sessionMu.RLock()
	dialURL := g.resumeURL
	sessID := g.sessionID
	g.sessionMu.RUnlock()
	if dialURL == "" {
		dialURL = g.url
	}

	g.statusMu.Lock()
	g.status = gateway.StatusConnecting
	g.statusMu.Unlock()

	dialer := websocket.DefaultDialer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, resp, err := dialer.DialContext(ctx, dialURL, nil)
	cancel()
	if resp != nil && resp.Body != nil {
		// Close HTTP response body to avoid descriptor leak.
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
		_ = conn.Close()
		return fmt.Errorf("safegw: read hello: %w", err)
	}

	var helloMsg gateway.Message
	if err := json.Unmarshal(helloData, &helloMsg); err != nil {
		ready(err)
		_ = conn.Close()
		return fmt.Errorf("safegw: unmarshal hello: %w", err)
	}
	if helloMsg.Op != gateway.OpcodeHello {
		err := fmt.Errorf("safegw: expected OpcodeHello, got %d", helloMsg.Op)
		ready(err)
		_ = conn.Close()
		return err
	}

	hello, ok := helloMsg.D.(gateway.MessageDataHello)
	if !ok {
		err := errors.New("safegw: invalid hello payload")
		ready(err)
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
			_ = conn.Close()
			return fmt.Errorf("safegw: send identify: %w", err)
		}
	}

	// 3. Read loop until error or reconnect.
	defer func() {
		g.stopHeartbeat()
		g.connMu.Lock()
		if g.conn == conn {
			_ = g.conn.Close()
			g.conn = nil
		}
		g.connMu.Unlock()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			g.statusMu.RLock()
			status := g.status
			g.statusMu.RUnlock()
			if status != gateway.StatusReady {
				ready(err)
			}
			return fmt.Errorf("safegw: read: %w", err)
		}

		var msg gateway.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Op {
		case gateway.OpcodeDispatch:
			g.seqMu.Lock()
			g.lastSeq = msg.S
			g.seqMu.Unlock()

			eventData, _ := msg.D.(gateway.EventData)
			if readyEvent, ok := eventData.(gateway.EventReady); ok {
				g.sessionMu.Lock()
				g.sessionID = readyEvent.SessionID
				g.resumeURL = readyEvent.ResumeGatewayURL
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
			return errors.New("safegw: server requested reconnect")

		case gateway.OpcodeInvalidSession:
			g.sessionMu.Lock()
			g.sessionID = ""
			g.resumeURL = ""
			g.sessionMu.Unlock()
			return errors.New("safegw: invalid session")
		}
	}
}

func (g *safeGateway) Send(_ context.Context, op gateway.Opcode, data gateway.MessageData) error {
	frame := gateway.Message{
		Op: op,
		D:  data,
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("safegw: marshal message: %w", err)
	}

	g.connMu.Lock()
	defer g.connMu.Unlock()
	if g.conn == nil {
		return net.ErrClosed
	}
	if err := g.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("safegw: write: %w", err)
	}
	return nil
}

func (g *safeGateway) Close(ctx context.Context) {
	g.CloseWithCode(ctx, websocket.CloseNormalClosure, "Shutting down")
}

func (g *safeGateway) CloseWithCode(_ context.Context, code int, message string) {
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
		_ = g.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, message),
			time.Now().Add(time.Second))
		_ = g.conn.Close()
		g.conn = nil
	}
	g.connMu.Unlock()

	g.statusMu.Lock()
	g.status = gateway.StatusDisconnected
	g.statusMu.Unlock()

	if g.closeHandler != nil {
		g.closeHandler(g, net.ErrClosed, false)
	}
}

func (g *safeGateway) stopHeartbeat() {
	g.heartbeatMu.Lock()
	if g.heartbeatCancel != nil {
		g.heartbeatCancel()
		g.heartbeatCancel = nil
	}
	g.heartbeatMu.Unlock()
}

func (g *safeGateway) sendHeartbeat() {
	g.seqMu.RLock()
	seq := g.lastSeq
	g.seqMu.RUnlock()

	g.heartbeatSentMu.Lock()
	g.lastHeartbeatSent = time.Now()
	g.heartbeatSentMu.Unlock()

	_ = g.Send(context.Background(), gateway.OpcodeHeartbeat, gateway.MessageDataHeartbeat(seq))
}

func (g *safeGateway) heartbeatLoop(ctx context.Context, interval time.Duration) {
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
