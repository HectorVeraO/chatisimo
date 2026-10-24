// Package fakediscord is an in-process fake of the parts of Discord's REST API and Gateway
// that the Chatisimo bot uses. A real Discord client library connects to it over HTTP and
// WebSocket, and it records every message the bot sends together with who could see it and
// whom it pinged, so tests can assert on what a person would experience.
//
// The fake holds only synthetic data. Its bot token is generated at start, lives in memory
// and is never logged.
package fakediscord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"text/template"
	"time"
)

const (
	defaultHeartbeatInterval = 41250 * time.Millisecond
	defaultCallbackDeadline  = 3 * time.Second
	restPrefix               = "/api/v10"
	gatewayPath              = "/ws"
	readHeaderTimeout        = 5 * time.Second
)

// ErrClosed is returned by operations on a Server that has been closed.
var ErrClosed = errors.New("fakediscord: server closed")

// Options configures a Server.
type Options struct {
	// HeartbeatInterval is the Gateway heartbeat interval sent in Hello. Zero means 41.25 s.
	HeartbeatInterval time.Duration
	// CallbackDeadline is how long the bot has to answer an interaction before its token
	// expires. Zero means 3 s, Discord's own limit.
	CallbackDeadline time.Duration
	// Logger receives one line per request (method, route pattern, status) and Gateway
	// lifecycle lines. Nil discards them. Nothing logged contains a token, header or body.
	Logger *slog.Logger
}

// Server is the fake Discord. Its methods are safe for concurrent use.
type Server struct {
	heartbeatMillis  int64
	callbackDeadline time.Duration
	logger           *slog.Logger
	appID            string
	token            string
	ids              *snowflakes
	templates        *template.Template
	handler          http.Handler

	mu       sync.Mutex
	w        *world
	sessions map[string]*session
	conns    map[*wsConn]struct{}
	// sessionSeq numbers sessions so an interaction goes to the newest one.
	sessionSeq int
	// recordSeq numbers outbound records and never restarts, so a reader's ?since= cursor
	// stays valid across a reset.
	recordSeq int
	subs      map[*subscriber]struct{}
	faults    map[string][]fault
	botStatus string
	// stopped is closed by Close so waits and event streams end.
	stopped chan struct{}
	// changed is closed and replaced on every state change WaitReady cares about.
	changed    chan struct{}
	closed     bool
	baseURL    string
	httpServer *http.Server
	serveDone  chan struct{}
	stopWatch  func() bool

	wsWG      sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// positiveDuration resolves an option that takes the fallback when zero and must otherwise
// be at least 1ms.
func positiveDuration(name string, value, fallback time.Duration) (time.Duration, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s %v is negative", name, value)
	}
	if value == 0 {
		return fallback, nil
	}
	if value < time.Millisecond {
		return 0, fmt.Errorf("%s %v is below 1ms", name, value)
	}

	return value, nil
}

// New creates a Server with a fresh application ID and bot token.
func New(opts Options) (*Server, error) {
	interval, intervalErr := positiveDuration("heartbeat interval", opts.HeartbeatInterval, defaultHeartbeatInterval)
	deadline, deadlineErr := positiveDuration("callback deadline", opts.CallbackDeadline, defaultCallbackDeadline)
	optionsErr := errors.Join(intervalErr, deadlineErr)
	if optionsErr != nil {
		return nil, fmt.Errorf("new fake discord: %w", optionsErr)
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	templates, err := loadTemplates()
	if err != nil {
		return nil, fmt.Errorf("new fake discord: %w", err)
	}

	ids := newSnowflakes()
	appID := ids.next()
	token, err := newToken(appID)
	if err != nil {
		return nil, fmt.Errorf("new fake discord: %w", err)
	}

	s := &Server{
		heartbeatMillis:  interval.Milliseconds(),
		callbackDeadline: deadline,
		logger:           logger,
		appID:            appID,
		token:            token,
		ids:              ids,
		templates:        templates,
		w:                newWorld(),
		sessions:         map[string]*session{},
		conns:            map[*wsConn]struct{}{},
		changed:          make(chan struct{}),
		subs:             map[*subscriber]struct{}{},
		faults:           map[string][]fault{},
		botStatus:        botAbsent,
		stopped:          make(chan struct{}),
	}
	s.handler = s.buildHandler()

	return s, nil
}

// Handler serves the REST API under /api/v10 and the Gateway at /ws.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Start listens on a loopback port so tests can reach the fake over real sockets. The
// server stops when ctx ends or Close is called.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrClosed
	}
	if s.httpServer != nil {
		return errors.New("fakediscord: server already started")
	}

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start fake discord: %w", err)
	}

	httpServer := &http.Server{Handler: s.handler, ReadHeaderTimeout: readHeaderTimeout}
	done := make(chan struct{})
	s.httpServer = httpServer
	s.serveDone = done
	s.baseURL = "http://" + listener.Addr().String()

	go func() {
		defer close(done)
		serveErr := httpServer.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.logger.Error("fake discord serve failed", "error", serveErr)
		}
	}()

	s.stopWatch = context.AfterFunc(ctx, func() {
		// The context ending is the owner's stop signal; a close error has nowhere to go.
		_ = s.Close()
	})

	return nil
}

// Close closes every Gateway session and stops the listener started by Start. It is safe to
// call more than once.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.shutdown()
	})

	return s.closeErr
}

func (s *Server) shutdown() error {
	s.mu.Lock()
	s.closed = true
	close(s.stopped)
	live := make([]*wsConn, 0, len(s.conns))
	for c := range s.conns {
		live = append(live, c)
	}
	httpServer := s.httpServer
	serveDone := s.serveDone
	stopWatch := s.stopWatch
	s.notifyLocked()
	s.mu.Unlock()

	if stopWatch != nil {
		stopWatch()
	}
	for _, c := range live {
		c.forceClose()
	}

	var closeErr error
	if httpServer != nil {
		err := httpServer.Close()
		if err != nil {
			closeErr = fmt.Errorf("close fake discord: %w", err)
		}
		<-serveDone
	}
	s.wsWG.Wait()

	return closeErr
}

// URL is the base URL of the listener started by Start, such as http://127.0.0.1:41234. It is
// empty before Start.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.baseURL
}

// RESTBase is the REST base URL to give a Discord client library, URL() plus /api/v10.
func (s *Server) RESTBase() string {
	base := s.URL()
	if base == "" {
		return ""
	}

	return base + restPrefix
}

// Token is the bot token the fake accepts.
func (s *Server) Token() string {
	return s.token
}

// ApplicationID is the ID of the fake's application, which is also the bot user's ID.
func (s *Server) ApplicationID() string {
	return s.appID
}

// WaitReady returns once a bot session has identified, been sent every guild, and a global
// command set has been registered, or when ctx ends.
func (s *Server) WaitReady(ctx context.Context) error {
	for {
		s.mu.Lock()
		isClosed := s.closed
		isReady := s.readyLocked()
		changed := s.changed
		s.mu.Unlock()

		if isClosed {
			return ErrClosed
		}
		if isReady {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for bot: %w", ctx.Err())
		case <-changed:
		}
	}
}

func (s *Server) readyLocked() bool {
	return len(s.sessions) > 0 && s.w.globalSet
}

// notifyLocked wakes every WaitReady and WaitDM, and tells event subscribers when the bot's
// status changed. Callers hold s.mu.
func (s *Server) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
	s.publishBotStatusLocked()
}

// AddUser registers a user, generating an ID when u.ID is empty, and returns it.
func (s *Server) AddUser(u User) User {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := s.w.upsertUser(u, s.ids)
	s.publishWorldChangedLocked()

	return stored
}

// AddGuild creates a guild with one text channel and the given members, unregistered users
// among them being registered, and announces it to every bot session with GUILD_CREATE. A
// g.ID that already exists renames that guild instead and announces it with GUILD_UPDATE. The returned Guild has its ID and
// ChannelID set.
func (s *Server) AddGuild(g Guild, members ...User) Guild {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, isNew := s.w.upsertGuild(g, s.ids)
	for _, member := range members {
		err := s.w.addMember(stored.ID, member, s.ids)
		if err != nil {
			panic("fakediscord: guild vanished while adding members: " + err.Error())
		}
	}
	if isNew {
		s.announceGuildLocked(stored.ID, "GUILD_CREATE")
	} else {
		s.announceGuildLocked(stored.ID, "GUILD_UPDATE")
	}
	s.publishWorldChangedLocked()

	return stored
}

// AddMember adds u to the guild, registering u when unknown. It panics when the guild is not
// one of the fake's, like the other setup helpers: a test arranging a world that does not
// exist is a bug in the test.
func (s *Server) AddMember(g Guild, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.w.addMember(g.ID, u, s.ids)
	if err != nil {
		panic("fakediscord: " + err.Error())
	}
	s.publishWorldChangedLocked()
}

// Visible returns the messages of a channel that as can see: public ones, and ephemeral ones
// meant for as. It is empty when as cannot see the channel at all.
func (s *Server) Visible(as User, channelID string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.w.visibleMessages(as.ID, channelID)
}

// DMs returns the messages the bot sent to u in their DM channel.
func (s *Server) DMs(u User) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	channelID, found := s.w.dmByUser[u.ID]
	if !found {
		return nil
	}

	return s.w.visibleMessages(u.ID, channelID)
}

// Outbound returns a copy of every outbound record, oldest first.
func (s *Server) Outbound() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]Record, 0, len(s.w.records))
	for _, r := range s.w.records {
		records = append(records, cloneRecord(r))
	}

	return records
}

// Commands returns the application commands the bot has registered: the global set first,
// then each guild's, in registration order.
func (s *Server) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()

	commands := make([]Command, 0, len(s.w.commands))
	for _, command := range s.w.commands {
		if command.GuildID == "" {
			commands = append(commands, command)
		}
	}
	for _, command := range s.w.commands {
		if command.GuildID != "" {
			commands = append(commands, command)
		}
	}

	return commands
}

// Intents returns the intents of the last accepted Identify, and false when no bot has
// identified yet.
func (s *Server) Intents() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.w.intents, s.w.intentsSet
}
