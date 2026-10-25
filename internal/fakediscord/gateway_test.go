package fakediscord_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const gatewayTimeout = 30 * time.Second

func TestWaitReadyEndsWithItsContext(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err := srv.WaitReady(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitReady = %v, want context.DeadlineExceeded", err)
	}
}

func TestWaitReadyFailsOnAClosedServer(t *testing.T) {
	t.Parallel()
	srv := newFake(t, fakediscord.Options{})
	closeErr := srv.Close()
	if closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	err := srv.WaitReady(testContext(t, time.Second))

	if !errors.Is(err, fakediscord.ErrClosed) {
		t.Errorf("WaitReady error = %v, want ErrClosed", err)
	}
}

func TestStartedServerServesRealSockets(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	if !strings.HasPrefix(srv.URL(), "http://127.0.0.1:") {
		t.Errorf("URL = %q, want a loopback http URL", srv.URL())
	}
	if srv.RESTBase() != srv.URL()+"/api/v10" {
		t.Errorf("RESTBase = %q, want URL plus /api/v10", srv.RESTBase())
	}
	req, err := http.NewRequestWithContext(testContext(t, gatewayTimeout), http.MethodGet, srv.RESTBase()+"/gateway", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpClient := &http.Client{}
	defer httpClient.CloseIdleConnections()

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET /gateway: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body map[string]string
	decodeErr := json.NewDecoder(resp.Body).Decode(&body)
	if decodeErr != nil {
		t.Fatalf("decode: %v", decodeErr)
	}
	wantURL := "ws://" + strings.TrimPrefix(srv.URL(), "http://") + "/ws"
	if body["url"] != wantURL {
		t.Errorf("gateway url = %q, want %q", body["url"], wantURL)
	}
}

func TestStartTwiceFails(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})

	err := srv.Start(t.Context())

	if err == nil {
		t.Error("a second Start succeeded")
	}
}

func TestContextEndStopsTheServer(t *testing.T) {
	t.Parallel()
	srv := newFake(t, fakediscord.Options{})
	ctx, cancel := context.WithCancel(t.Context())
	err := srv.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	cancel()

	waitErr := srv.WaitReady(testContext(t, gatewayTimeout))
	if !errors.Is(waitErr, fakediscord.ErrClosed) {
		t.Errorf("WaitReady error = %v, want ErrClosed", waitErr)
	}
}

// rawGateway is a bare WebSocket connection to the fake's Gateway.
type rawGateway struct {
	ws *websocket.Conn
}

func dialRaw(t *testing.T, srv *fakediscord.Server) *rawGateway {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL(), "http") + "/ws"
	ws, resp, err := websocket.DefaultDialer.DialContext(testContext(t, gatewayTimeout), url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	closeErr := resp.Body.Close()
	if closeErr != nil {
		t.Fatalf("close upgrade body: %v", closeErr)
	}
	t.Cleanup(func() { _ = ws.Close() })

	g := &rawGateway{ws: ws}
	if hello := g.read(t); hello.Op != 10 {
		t.Fatalf("first frame op = %d, want Hello (10)", hello.Op)
	}

	return g
}

type rawFrame struct {
	Op int             `json:"op"`
	T  string          `json:"t"`
	D  json.RawMessage `json:"d"`
}

func (g *rawGateway) send(t *testing.T, op int, data string) {
	t.Helper()

	payload := `{"op":` + strconv.Itoa(op) + `,"d":` + data + `}`
	err := g.ws.WriteMessage(websocket.TextMessage, []byte(payload))
	if err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func (g *rawGateway) read(t *testing.T) rawFrame {
	t.Helper()

	err := g.ws.SetReadDeadline(time.Now().Add(gatewayTimeout))
	if err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var in rawFrame
	err = g.ws.ReadJSON(&in)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	return in
}

// readClose returns the close code the fake ends the connection with.
func (g *rawGateway) readClose(t *testing.T) int {
	t.Helper()

	for {
		err := g.ws.SetReadDeadline(time.Now().Add(gatewayTimeout))
		if err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, _, readErr := g.ws.ReadMessage()
		if readErr == nil {
			continue
		}
		var closeErr *websocket.CloseError
		if !errors.As(readErr, &closeErr) {
			t.Fatalf("connection ended without a close frame: %v", readErr)
		}

		return closeErr.Code
	}
}

// identifyRaw identifies and returns the session_id of the READY it receives.
func identifyRaw(t *testing.T, srv *fakediscord.Server) (*rawGateway, string) {
	t.Helper()

	g := dialRaw(t, srv)
	g.send(t, 2, `{"token":"`+srv.Token()+`","intents":1}`)
	ready := g.read(t)
	if ready.T != "READY" {
		t.Fatalf("frame after Identify = %s, want READY", ready.T)
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	decodeJSON(t, ready.D, &body)

	return g, body.SessionID
}

func TestResumeIsDenied(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	_, sessionID := identifyRaw(t, srv)
	segments := strings.Split(srv.Token(), ".")
	wrong := segments[0] + "." + strings.Repeat("a", 8) + "." + strings.Repeat("b", 27)

	t.Run("a wrong token is closed with authentication failed", func(t *testing.T) {
		g := dialRaw(t, srv)

		g.send(t, 6, `{"token":"`+wrong+`","session_id":"`+sessionID+`","seq":0}`)

		if code := g.readClose(t); code != 4004 {
			t.Errorf("close code = %d, want 4004", code)
		}
	})

	t.Run("an unknown session gets an invalid session", func(t *testing.T) {
		g := dialRaw(t, srv)

		g.send(t, 6, `{"token":"`+srv.Token()+`","session_id":"no-such-session","seq":0}`)

		if got := g.read(t); got.Op != 9 || string(got.D) != "false" {
			t.Errorf("frame = %+v, want op 9 with d false", got)
		}
	})

	t.Run("a sequence ahead of the session gets an invalid session", func(t *testing.T) {
		g := dialRaw(t, srv)

		g.send(t, 6, `{"token":"`+srv.Token()+`","session_id":"`+sessionID+`","seq":9999}`)

		if got := g.read(t); got.Op != 9 {
			t.Errorf("frame op = %d, want 9", got.Op)
		}
	})
}

func TestMalformedFramesAreRefused(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})

	tests := []struct {
		name        string
		messageType int
		payload     string
		wantCode    int
	}{
		{"a binary frame", websocket.BinaryMessage, `{"op":1,"d":null}`, 4002},
		{"a frame that is not JSON", websocket.TextMessage, `not json`, 4002},
		{"an identify with a malformed payload", websocket.TextMessage, `{"op":2,"d":"x"}`, 4002},
		{"a resume with a malformed payload", websocket.TextMessage, `{"op":6,"d":[1]}`, 4002},
		{"an identify without a token", websocket.TextMessage, `{"op":2,"d":{}}`, 4004},
		{"an unknown op before identifying", websocket.TextMessage, `{"op":99,"d":null}`, 4003},
	}
	for _, tt := range tests {
		t.Run(tt.name+" is closed with the matching code", func(t *testing.T) {
			g := dialRaw(t, srv)

			err := g.ws.WriteMessage(tt.messageType, []byte(tt.payload))
			if err != nil {
				t.Fatalf("write frame: %v", err)
			}

			if code := g.readClose(t); code != tt.wantCode {
				t.Errorf("close code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

func FuzzGatewayFrame(f *testing.F) {
	srv := startFake(f, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	seeds := []string{
		``, `{`, `null`, `[]`, `{}`, `{"op":1}`, `{"op":1,"d":null}`, `{"op":2,"d":null}`,
		`{"op":2,"d":{"token":"x","intents":"y"}}`, `{"op":6,"d":{"session_id":5}}`,
		`{"op":99,"d":{}}`, `{"op":"2"}`, `{"op":2,"d":{"token":"\u202e\u200b e\u0301 \ud83d\ude00"}}`,
		`{"op":6,"d":{"token":"a","session_id":"` + strings.Repeat("s", 4096) + `","seq":-1}}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload string) {
		g := dialRaw(t, srv)
		err := g.ws.WriteMessage(websocket.TextMessage, []byte(payload))
		if err != nil {
			t.Fatalf("write frame: %v", err)
		}

		// A frame the fake accepts as a heartbeat is acknowledged; anything else is
		// refused with a close or ignored. Either way the fake must keep serving.
		probe := dialRaw(t, srv)
		probe.send(t, 1, `null`)
		if got := probe.read(t); got.Op != 11 {
			t.Fatalf("heartbeat after payload %q: op = %d, want 11", payload, got.Op)
		}
	})
}

func TestRenamingAGuildAnnouncesAnUpdate(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	g, _ := identifyRaw(t, srv)
	if created := g.read(t); created.T != "GUILD_CREATE" {
		t.Fatalf("frame after READY = %s, want GUILD_CREATE", created.T)
	}

	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Deep winter guild"})

	updated := g.read(t)
	if updated.T != "GUILD_UPDATE" {
		t.Fatalf("frame after rename = %s, want GUILD_UPDATE", updated.T)
	}
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decodeJSON(t, updated.D, &body)
	if body.ID != winterGuildID || body.Name != "Deep winter guild" {
		t.Errorf("GUILD_UPDATE = %+v, want the renamed winter guild", body)
	}
}

// assertTokenStaysPrivate connects a Gateway client with connect, drives REST traffic and checks
// that neither the token nor its segments reach the outbound records or the fake's logs.
func assertTokenStaysPrivate(t *testing.T, connect func(t *testing.T, srv *fakediscord.Server)) {
	t.Helper()
	var logs syncBuffer
	srv := startFake(t, fakediscord.Options{Logger: capturingLogger(&logs)})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	connect(t, srv)
	posted := postChannelMessage(t, srv, guild.ChannelID, "hello "+"<@"+aliceID+">", "", "")
	if posted.status != http.StatusOK {
		t.Fatalf("post status = %d, body %s", posted.status, posted.body)
	}
	openedDM := openDM(t, srv, aliceID)
	call(t, srv, http.MethodPost, "/api/v10/channels/"+openedDM+"/messages", botAuth(srv), `{"content":"dm"}`)
	call(t, srv, http.MethodPost, "/api/v10/channels/"+openedDM+"/messages", "Bot "+srv.Token()+"x", `{"content":"dm"}`)
	err := srv.WaitReady(testContext(t, gatewayTimeout))
	if err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	outbound, err := json.Marshal(srv.Outbound())
	if err != nil {
		t.Fatalf("marshal outbound: %v", err)
	}
	if len(srv.Outbound()) < 2 {
		t.Fatalf("outbound holds %d records, want the channel and DM posts", len(srv.Outbound()))
	}
	segments := strings.Split(srv.Token(), ".")
	secrets := map[string]string{"token": srv.Token(), "second segment": segments[1], "third segment": segments[2]}
	for name, secret := range secrets {
		if strings.Contains(string(outbound), secret) {
			t.Errorf("the outbound record contains the %s", name)
		}
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the fake's logs contain the %s", name)
		}
	}
	if logs.String() == "" {
		t.Error("the fake logged nothing, so the check above proves nothing")
	}
}

func TestTokenNeverReachesRecordsOrLogsOverRawGateway(t *testing.T) {
	t.Parallel()
	assertTokenStaysPrivate(t, func(t *testing.T, srv *fakediscord.Server) {
		t.Helper()

		identifyRaw(t, srv)
		registerPing(t, srv)
	})
}

// registerPing sets the application's global commands over REST.
func registerPing(t *testing.T, srv *fakediscord.Server) {
	t.Helper()

	path := "/api/v10/applications/" + srv.ApplicationID() + "/commands"
	got := call(t, srv, http.MethodPut, path, botAuth(srv), `[{"name":"ping","description":"Ping the bot","type":1}]`)
	if got.status != http.StatusOK {
		t.Fatalf("PUT commands status = %d, body %s", got.status, got.body)
	}
}

func TestHeartbeatIsAcknowledged(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	g, _ := identifyRaw(t, srv)

	g.send(t, 1, `null`)

	for {
		frame := g.read(t)
		if frame.Op == 11 {
			break
		}
	}
	if got := srv.HeartbeatAcks(); got != 1 {
		t.Errorf("HeartbeatAcks() = %d, want 1", got)
	}
}

func TestWrongTokenIdentifyIsClosedWithAuthenticationFailed(t *testing.T) {
	t.Parallel()
	var logs syncBuffer
	srv := startFake(t, fakediscord.Options{Logger: capturingLogger(&logs)})
	segments := strings.Split(srv.Token(), ".")
	wrong := segments[0] + "." + strings.Repeat("a", 8) + "." + strings.Repeat("b", 27)
	g := dialRaw(t, srv)

	g.send(t, 2, `{"token":"`+wrong+`","intents":1}`)

	if code := g.readClose(t); code != 4004 {
		t.Errorf("close code = %d, want 4004", code)
	}
	if _, identified := srv.Intents(); identified {
		t.Error("the fake recorded intents for a rejected Identify")
	}
	waitFor(t, gatewayTimeout, "the rejected identify to be logged", func() bool {
		return strings.Contains(logs.String(), "accepted=false")
	})
	if n := strings.Count(logs.String(), "gateway identify"); n != 1 {
		t.Errorf("the fake saw %d identifies, want 1:\n%s", n, logs.String())
	}
}

func TestWaitReadyNeedsAGlobalCommandSetOverRawGateway(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	identifyRaw(t, srv)

	early, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := srv.WaitReady(early); err == nil {
		t.Fatal("WaitReady returned before any global command set was registered")
	}

	registerPing(t, srv)
	err := srv.WaitReady(testContext(t, gatewayTimeout))
	if err != nil {
		t.Errorf("WaitReady after registering commands: %v", err)
	}
}
