package safegw_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/gateway"
	"github.com/gorilla/websocket"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/safegw"
)

const testBotToken = "synthetic-bot-token"

func TestIsValidRedirectURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rawURL  string
		baseURL string
		want    bool
	}{
		{
			name:    "accepts identical local ws host and port with different path",
			rawURL:  "ws://127.0.0.1:10010/resume?v=10&encoding=json",
			baseURL: "ws://127.0.0.1:10010/ws",
			want:    true,
		},
		{
			name:    "accepts regional discord.gg subdomain when base is wss gateway.discord.gg",
			rawURL:  "wss://gateway-us-east1-b.discord.gg/?v=10&encoding=json",
			baseURL: "wss://gateway.discord.gg/?v=10&encoding=json",
			want:    true,
		},
		{
			name:    "accepts explicit port 443 on wss discord.gg subdomain",
			rawURL:  "wss://gateway-us-east1-b.discord.gg:443/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    true,
		},
		{
			name:    "accepts apex discord.gg over wss",
			rawURL:  "wss://discord.gg/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    true,
		},
		{
			name:    "rejects empty raw URL",
			rawURL:  "",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects empty base URL",
			rawURL:  "wss://gateway.discord.gg/ws",
			baseURL: "",
			want:    false,
		},
		{
			name:    "rejects local ws redirect to a different port",
			rawURL:  "ws://127.0.0.1:19999/ws",
			baseURL: "ws://127.0.0.1:10010/ws",
			want:    false,
		},
		{
			name:    "rejects local ws redirect to a different host",
			rawURL:  "ws://169.254.169.254/latest/meta-data",
			baseURL: "ws://127.0.0.1:10010/ws",
			want:    false,
		},
		{
			name:    "rejects scheme downgrade from wss to ws",
			rawURL:  "ws://gateway-us-east1-b.discord.gg/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects non-websocket http scheme",
			rawURL:  "https://gateway.discord.gg/ws",
			baseURL: "https://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects untrusted external domain",
			rawURL:  "wss://attacker.example.com/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects suffix-spoofed discord.gg domain",
			rawURL:  "wss://discord.gg.example.com/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects non-standard port on discord.gg subdomain",
			rawURL:  "wss://gateway-us-east1-b.discord.gg:8443/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects URL with userinfo",
			rawURL:  "ws://user@127.0.0.1:10010/ws",
			baseURL: "ws://127.0.0.1:10010/ws",
			want:    false,
		},
		{
			name:    "rejects URL with fragment",
			rawURL:  "wss://gateway.discord.gg/ws#fragment",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects URL with backslash authority confusion",
			rawURL:  `wss://attacker.example.com\@gateway.discord.gg/ws`,
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects opaque URL without double slash",
			rawURL:  "wss:gateway.discord.gg",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects malformed empty subdomain label",
			rawURL:  "wss://..discord.gg/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
		{
			name:    "rejects subdomain label starting with hyphen",
			rawURL:  "wss://-gateway.discord.gg/ws",
			baseURL: "wss://gateway.discord.gg/ws",
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := safegw.IsValidRedirectURL(tc.rawURL, tc.baseURL)
			if got != tc.want {
				t.Errorf("IsValidRedirectURL(%q, %q) = %v, want %v", tc.rawURL, tc.baseURL, got, tc.want)
			}
		})
	}
}

func FuzzIsValidRedirectURL(f *testing.F) {
	seeds := []struct {
		rawURL  string
		baseURL string
	}{
		{"wss://gateway-us-east1-b.discord.gg/?v=10", "wss://gateway.discord.gg/?v=10"},
		{"ws://127.0.0.1:10010/ws", "ws://127.0.0.1:10010/ws"},
		{"ws://169.254.169.254/latest", "ws://127.0.0.1:10010/ws"},
		{"wss://discord.gg.example.com/ws", "wss://gateway.discord.gg/ws"},
		{"ws://user@127.0.0.1:10010/ws", "ws://127.0.0.1:10010/ws"},
		{`wss://evil.example.com\@gateway.discord.gg`, "wss://gateway.discord.gg/ws"},
		{"wss://gateway.discord.gg/ws#frag", "wss://gateway.discord.gg/ws"},
		{"http://127.0.0.1:10010/ws", "http://127.0.0.1:10010/ws"},
	}
	for _, s := range seeds {
		f.Add(s.rawURL, s.baseURL)
	}

	f.Fuzz(func(t *testing.T, rawURL, baseURL string) {
		valid := safegw.IsValidRedirectURL(rawURL, baseURL)
		if !valid {
			return
		}

		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("IsValidRedirectURL accepted unparseable URL %q: %v", rawURL, err)
		}
		if parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
			t.Fatalf("IsValidRedirectURL accepted URL with userinfo, fragment, or opaque part: %q", rawURL)
		}
		isWS := strings.EqualFold(parsed.Scheme, "ws") || strings.EqualFold(parsed.Scheme, "wss")
		if !isWS {
			t.Fatalf("IsValidRedirectURL accepted non-websocket scheme %q in %q", parsed.Scheme, rawURL)
		}
	})
}

func TestGatewayReconnectsToValidatedResumeURL(t *testing.T) {
	t.Parallel()

	var resumeHits atomic.Int32
	resumed := make(chan struct{}, 1)
	var srvURL string

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() {
			// Best-effort close on test server teardown.
			_ = ws.Close()
		}()

		hello := map[string]any{
			"op": 10,
			"d":  map[string]any{"heartbeat_interval": 60000},
		}
		if err := ws.WriteJSON(hello); err != nil {
			return
		}

		_, rawFrame, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var incoming struct {
			Op int `json:"op"`
		}
		if err := json.Unmarshal(rawFrame, &incoming); err != nil {
			return
		}

		if r.URL.Path == "/resume" && incoming.Op == int(gateway.OpcodeResume) {
			resumeHits.Add(1)
			resumedFrame := map[string]any{
				"op": 0,
				"s":  2,
				"t":  "RESUMED",
				"d":  map[string]any{},
			}
			// Best-effort write in test handler.
			_ = ws.WriteJSON(resumedFrame)
			select {
			case resumed <- struct{}{}:
			default:
			}
			_, _, _ = ws.ReadMessage()
			return
		}

		readyFrame := map[string]any{
			"op": 0,
			"s":  1,
			"t":  "READY",
			"d": map[string]any{
				"v":                  10,
				"session_id":         "sess-valid-1",
				"resume_gateway_url": srvURL + "/resume",
				"user": map[string]any{
					"id":            "100000000000000001",
					"username":      "chatisimo-test",
					"discriminator": "0000",
					"bot":           true,
				},
				"guilds": []any{},
				"application": map[string]any{
					"id":    "200000000000000001",
					"flags": 0,
				},
			},
		}
		if err := ws.WriteJSON(readyFrame); err != nil {
			return
		}

		reconnectFrame := map[string]any{
			"op": 7,
		}
		// Trigger client reconnect to the resume URL.
		_ = ws.WriteJSON(reconnectFrame)
		_, _, _ = ws.ReadMessage()
	}))
	defer srv.Close()

	srvURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	gw := safegw.New(testBotToken, srvURL+"/ws", gateway.IntentGuilds, nil)
	defer gw.Close(context.Background())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := gw.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	gotResume := gw.ResumeURL()
	wantResume := srvURL + "/resume"
	if gotResume == nil || *gotResume != wantResume {
		t.Fatalf("ResumeURL() = %v, want %q", gotResume, wantResume)
	}

	select {
	case <-resumed:
	case <-ctx.Done():
		t.Fatal("timed out waiting for gateway to resume on validated path")
	}

	if hits := resumeHits.Load(); hits != 1 {
		t.Errorf("resume path hits = %d, want 1", hits)
	}
}

func TestGatewayRejectsUntrustedResumeURLOnReconnect(t *testing.T) {
	t.Parallel()

	var trapHits atomic.Int32
	trapSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		trapHits.Add(1)
	}))
	defer trapSrv.Close()
	trapWSURL := "ws" + strings.TrimPrefix(trapSrv.URL, "http") + "/steal-token"

	var primaryConnects atomic.Int32
	reconnectedToPrimary := make(chan struct{}, 1)

	upgrader := websocket.Upgrader{}
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := primaryConnects.Add(1)
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() {
			// Best-effort close on test server teardown.
			_ = ws.Close()
		}()

		hello := map[string]any{
			"op": 10,
			"d":  map[string]any{"heartbeat_interval": 60000},
		}
		if err := ws.WriteJSON(hello); err != nil {
			return
		}

		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}

		readyFrame := map[string]any{
			"op": 0,
			"s":  1,
			"t":  "READY",
			"d": map[string]any{
				"v":                  10,
				"session_id":         "sess-untrusted-1",
				"resume_gateway_url": trapWSURL,
				"user": map[string]any{
					"id":            "100000000000000001",
					"username":      "chatisimo-test",
					"discriminator": "0000",
					"bot":           true,
				},
				"guilds": []any{},
				"application": map[string]any{
					"id":    "200000000000000001",
					"flags": 0,
				},
			},
		}
		if err := ws.WriteJSON(readyFrame); err != nil {
			return
		}

		if attempt == 1 {
			reconnectFrame := map[string]any{
				"op": 7,
			}
			// Trigger client reconnect after sending untrusted resume_gateway_url.
			_ = ws.WriteJSON(reconnectFrame)
		} else {
			select {
			case reconnectedToPrimary <- struct{}{}:
			default:
			}
		}

		_, _, _ = ws.ReadMessage()
	}))
	defer primarySrv.Close()

	primaryWSURL := "ws" + strings.TrimPrefix(primarySrv.URL, "http") + "/ws"
	gw := safegw.New(testBotToken, primaryWSURL, gateway.IntentGuilds, nil)
	defer gw.Close(context.Background())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := gw.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if gotResume := gw.ResumeURL(); gotResume != nil {
		t.Errorf("ResumeURL() = %q, want nil for untrusted resume_gateway_url", *gotResume)
	}

	select {
	case <-reconnectedToPrimary:
	case <-ctx.Done():
		t.Fatal("timed out waiting for gateway to reconnect to primary URL")
	}

	if gotTrap := trapHits.Load(); gotTrap != 0 {
		t.Errorf("trap server hits = %d, want 0 (untrusted resume_gateway_url must never be dialed)", gotTrap)
	}
}
