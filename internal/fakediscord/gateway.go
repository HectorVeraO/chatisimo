package fakediscord

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Gateway opcodes.
const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatACK   = 11
)

// Gateway close codes.
const (
	closeDecodeError       = 4002
	closeNotAuthenticated  = 4003
	closeAuthFailed        = 4004
	closeAlreadyIdentified = 4005
	closeSessionMoved      = 4000
)

const (
	gatewayVersion  = 10
	maxQueuedFrames = 1 << 16
	maxFrameBytes   = 1 << 20
	closeGrace      = 2 * time.Second
	sessionIDBytes  = 16
	normalCloseText = "going away"
)

// frame is a Gateway payload, in either direction.
type frame struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int            `json:"s,omitempty"`
	T  *string         `json:"t,omitempty"`
}

// sentDispatch is a dispatch kept so a resuming client can be sent what it missed.
type sentDispatch struct {
	seq  int
	data []byte
}

// session is one bot Gateway session. It outlives the connection that created it, so a
// client that drops can resume on a new connection.
type session struct {
	id string
	// order numbers sessions by when they identified.
	order   int
	seq     int
	history []sentDispatch
	conn    *wsConn
}

// wsConn is one WebSocket connection. Writes go through send and a single writer goroutine,
// because gorilla/websocket allows one concurrent writer.
type wsConn struct {
	ws   *websocket.Conn
	host string
	done chan struct{}
	wake chan struct{}
	// queueMu guards queue, the frames waiting for the writer.
	queueMu sync.Mutex
	queue   [][]byte
	closing atomic.Bool
	// session is guarded by Server.mu.
	session *session
}

func (c *wsConn) enqueue(data []byte) {
	c.queueMu.Lock()
	isFull := len(c.queue) >= maxQueuedFrames
	if !isFull {
		c.queue = append(c.queue, data)
	}
	c.queueMu.Unlock()

	if isFull {
		// A client that does not read is dropped rather than growing the queue without bound.
		c.forceClose()
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *wsConn) takeQueued() [][]byte {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()

	pending := c.queue
	c.queue = nil

	return pending
}

func (c *wsConn) writeLoop(finished chan<- struct{}) {
	defer close(finished)
	for {
		select {
		case <-c.wake:
		case <-c.done:
			return
		}
		for _, data := range c.takeQueued() {
			err := c.ws.WriteMessage(websocket.TextMessage, data)
			if err != nil {
				c.forceClose()
				return
			}
		}
	}
}

// closeWith sends a close frame with the given code and gives the client a moment to answer
// before the read loop gives up.
func (c *wsConn) closeWith(code int, text string) {
	c.closing.Store(true)
	deadline := time.Now().Add(closeGrace)
	// If the connection is already broken the read loop ends on its own.
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), deadline)
	// A deadline error means the connection is already gone, and the read loop ends with it.
	_ = c.ws.SetReadDeadline(deadline)
}

// forceClose closes the underlying connection at once, ending the read loop.
func (c *wsConn) forceClose() {
	c.closing.Store(true)
	deadline := time.Now().Add(time.Second)
	// Best effort: the connection is being torn down either way.
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, normalCloseText), deadline)
	_ = c.ws.Close()
}

// DropGateway closes every bot connection with the given close code. Sessions are kept, so
// a client that treats the code as resumable can resume them.
func (s *Server) DropGateway(code int) {
	s.mu.Lock()
	live := make([]*wsConn, 0, len(s.conns))
	for c := range s.conns {
		live = append(live, c)
	}
	s.mu.Unlock()

	for _, c := range live {
		c.closeWith(code, "dropped")
	}
}

func (s *Server) registerConn(c *wsConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	s.wsWG.Add(1)

	return true
}

func (s *Server) unregisterConn(c *wsConn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.conns, c)
	if c.session != nil && c.session.conn == c {
		c.session.conn = nil
	}
}

// serveGateway upgrades the request and runs the Gateway protocol on the connection.
func (s *Server) serveGateway(w http.ResponseWriter, r *http.Request) {
	var upgrader websocket.Upgrader
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Debug("gateway upgrade refused", "error", err)
		return
	}

	conn := &wsConn{
		ws:   ws,
		host: requestHost(r),
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	if !s.registerConn(conn) {
		// The server is closing and the connection was never served; a close error changes nothing.
		_ = ws.Close()
		return
	}
	defer s.wsWG.Done()

	writerDone := make(chan struct{})
	go conn.writeLoop(writerDone)
	defer func() {
		close(conn.done)
		// A writer stuck on a client that stopped reading is released by closing the socket.
		select {
		case <-writerDone:
		case <-time.After(closeGrace):
			_ = ws.Close()
			<-writerDone
		}
		s.unregisterConn(conn)
		// The connection is finished; a close error changes nothing.
		_ = ws.Close()
	}()

	ws.SetReadLimit(maxFrameBytes)
	s.sendHello(conn)
	s.readLoop(conn)
}

func requestHost(r *http.Request) string {
	if r.Host == "" {
		return "127.0.0.1"
	}

	return r.Host
}

func (s *Server) sendHello(c *wsConn) {
	hello, err := json.Marshal(map[string]int64{"heartbeat_interval": s.heartbeatMillis})
	if err != nil {
		panic("fakediscord: hello is not marshalable: " + err.Error())
	}
	c.enqueue(mustMarshalFrame(frame{Op: opHello, D: hello}))
}

func (s *Server) readLoop(c *wsConn) {
	for {
		messageType, data, err := c.ws.ReadMessage()
		if err != nil {
			s.logger.Debug("gateway connection ended")
			return
		}
		if c.closing.Load() {
			continue
		}
		if messageType != websocket.TextMessage {
			c.closeWith(closeDecodeError, "text frames only")
			continue
		}

		var in frame
		decodeErr := json.Unmarshal(data, &in)
		if decodeErr != nil {
			c.closeWith(closeDecodeError, "invalid frame")
			continue
		}
		s.handleFrame(c, in)
	}
}

func (s *Server) handleFrame(c *wsConn, in frame) {
	switch in.Op {
	case opHeartbeat:
		// Count before sending, so a reader that has seen the ACK sees the count too.
		s.mu.Lock()
		s.heartbeatAcks++
		s.mu.Unlock()
		c.enqueue(mustMarshalFrame(frame{Op: opHeartbeatACK}))
	case opIdentify:
		s.handleIdentify(c, in.D)
	case opResume:
		s.handleResume(c, in.D)
	default:
		isIdentified := s.isIdentified(c)
		if !isIdentified {
			c.closeWith(closeNotAuthenticated, "not authenticated")
		}
	}
}

func (s *Server) isIdentified(c *wsConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return c.session != nil
}

func (s *Server) handleIdentify(c *wsConn, data json.RawMessage) {
	var identify struct {
		Token   string `json:"token"`
		Intents int    `json:"intents"`
	}
	err := json.Unmarshal(data, &identify)
	if err != nil {
		c.closeWith(closeDecodeError, "invalid identify")
		return
	}
	injected, isInjected := s.takeFault(routeGatewayIdentify)
	if isInjected {
		s.logger.Info("fault injected", "route", routeGatewayIdentify, "code", injected.code)
		c.closeWith(injected.code, injectedFaultMessage)
		return
	}
	if !tokensEqual(identify.Token, s.token) {
		s.logger.Info("gateway identify", "accepted", false)
		c.closeWith(closeAuthFailed, "Authentication failed.")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if c.session != nil {
		c.closeWith(closeAlreadyIdentified, "already authenticated")
		return
	}

	s.sessionSeq++
	sess := &session{id: newSessionID(), order: s.sessionSeq, conn: c}
	s.sessions[sess.id] = sess
	c.session = sess
	s.w.intents = identify.Intents
	s.w.intentsSet = true

	s.dispatchReadyLocked(sess, c.host)
	s.notifyLocked()
	s.logger.Info("gateway identify", "accepted", true, "intents", identify.Intents)
}

// dispatchReadyLocked sends READY and then one GUILD_CREATE per guild.
func (s *Server) dispatchReadyLocked(sess *session, host string) {
	unavailable := make([]map[string]any, 0, len(s.w.guildOrder))
	for _, guildID := range s.w.guildOrder {
		unavailable = append(unavailable, map[string]any{"id": guildID, "unavailable": true})
	}

	s.dispatchLocked(sess, "READY", map[string]any{
		"v":                  gatewayVersion,
		"user":               s.botUser(),
		"guilds":             unavailable,
		"session_id":         sess.id,
		"resume_gateway_url": "ws://" + host + gatewayPath,
		"application":        map[string]any{"id": s.appID, "flags": 0},
	})
	for _, guildID := range s.w.guildOrder {
		s.dispatchGuildLocked(sess, "GUILD_CREATE", guildID)
	}
}

// announceGuildLocked pushes a guild event (GUILD_CREATE or GUILD_UPDATE) for a guild
// changed after READY to every session.
func (s *Server) announceGuildLocked(guildID, event string) {
	for _, sess := range s.sessions {
		s.dispatchGuildLocked(sess, event, guildID)
	}
}

// dispatchGuildLocked sends a guild without channels: the fake's channel is a text
// channel the bot learns about from interactions, and a non-guild channel type would panic
// the client library.
func (s *Server) dispatchGuildLocked(sess *session, event, guildID string) {
	guild := s.w.guilds[guildID]
	s.dispatchLocked(sess, event, map[string]any{
		"id":          guild.ID,
		"name":        guild.Name,
		"owner_id":    s.ownerIDLocked(guild),
		"unavailable": false,
	})
}

func (s *Server) ownerIDLocked(guild *guildState) string {
	if len(guild.members) > 0 {
		return guild.members[0]
	}

	return s.appID
}

// dispatchLocked numbers a dispatch, keeps it for resuming and sends it when the session is
// connected.
func (s *Server) dispatchLocked(sess *session, name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		panic("fakediscord: dispatch " + name + " is not marshalable: " + err.Error())
	}

	sess.seq++
	seq := sess.seq
	encoded := mustMarshalFrame(frame{Op: opDispatch, D: data, S: &seq, T: &name})
	sess.history = append(sess.history, sentDispatch{seq: seq, data: encoded})
	if sess.conn != nil {
		sess.conn.enqueue(encoded)
	}
}

func (s *Server) handleResume(c *wsConn, data json.RawMessage) {
	var resume struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Seq       int    `json:"seq"`
	}
	err := json.Unmarshal(data, &resume)
	if err != nil {
		c.closeWith(closeDecodeError, "invalid resume")
		return
	}
	if !tokensEqual(resume.Token, s.token) {
		s.logger.Info("gateway resume", "accepted", false)
		c.closeWith(closeAuthFailed, "Authentication failed.")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, found := s.sessions[resume.SessionID]
	isResumable := found && resume.Seq >= 0 && resume.Seq <= sess.seq && c.session == nil
	if !isResumable {
		s.logger.Info("gateway resume", "accepted", false)
		c.enqueue(mustMarshalFrame(frame{Op: opInvalidSession, D: json.RawMessage("false")}))
		return
	}

	previous := sess.conn
	sess.conn = c
	c.session = sess
	if previous != nil && previous != c {
		previous.closeWith(closeSessionMoved, "session resumed elsewhere")
	}

	for _, missed := range sess.history {
		if missed.seq > resume.Seq {
			c.enqueue(missed.data)
		}
	}
	s.dispatchLocked(sess, "RESUMED", map[string]any{})
	s.resumes++
	s.logger.Info("gateway resume", "accepted", true)
}

func newSessionID() string {
	buf := make([]byte, sessionIDBytes)
	_, err := rand.Read(buf)
	if err != nil {
		panic(fmt.Sprintf("fakediscord: read random bytes: %v", err))
	}

	return hex.EncodeToString(buf)
}

func mustMarshalFrame(f frame) []byte {
	data, err := json.Marshal(f)
	if err != nil {
		panic("fakediscord: frame is not marshalable: " + err.Error())
	}

	return data
}
