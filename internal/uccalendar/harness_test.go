package uccalendar_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

//go:embed testdata/*.json
var testdataFS embed.FS

const (
	fakeFriendID  = "100000000000000001"
	otherFriendID = "100000000000000002"
	fakeChannelID = "300000000000000001"
)

// syncBuffer is a bytes.Buffer safe for concurrent writes from slog and reads from tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

type fakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newFakeClock(initial time.Time) *fakeClock {
	return &fakeClock{now: initial}
}

func (c *fakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type mapSettings struct {
	mu     sync.RWMutex
	values map[string]string
	err    error
}

func newMapSettings(initial map[string]string) *mapSettings {
	cloned := make(map[string]string, len(initial))
	for k, v := range initial {
		cloned[k] = v
	}

	return &mapSettings{values: cloned}
}

func (m *mapSettings) Get(_ context.Context, name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return "", m.err
	}

	val, ok := m.values[name]
	if !ok || val == "" {
		return "", config.ErrNotSet
	}

	return val, nil
}

func (m *mapSettings) SetValue(name, value string) {
	m.mu.Lock()
	if value == "" {
		delete(m.values, name)
	} else {
		m.values[name] = value
	}
	m.mu.Unlock()
}

type fakeESPNServer struct {
	server        *httptest.Server
	mu            sync.RWMutex
	completedJSON []byte
	upcomingJSON  []byte
	statusCode    int
	requests      int
}

func newFakeESPNServer(t *testing.T, completedJSON, upcomingJSON []byte) *fakeESPNServer {
	t.Helper()

	fake := &fakeESPNServer{
		completedJSON: completedJSON,
		upcomingJSON:  upcomingJSON,
		statusCode:    http.StatusOK,
	}

	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests++
		status := fake.statusCode
		completed := fake.completedJSON
		upcoming := fake.upcomingJSON
		fake.mu.Unlock()

		if r.URL.Path != "/apis/site/v2/sports/soccer/all/teams/885/schedule" {
			http.NotFound(w, r)
			return
		}

		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fixture") == "true" {
			_, _ = w.Write(upcoming)
			return
		}

		_, _ = w.Write(completed)
	}))

	t.Cleanup(fake.server.Close)

	return fake
}

func (f *fakeESPNServer) BaseURL() string {
	return f.server.URL + "/apis/site/v2/sports/soccer"
}

func (f *fakeESPNServer) SetStatus(code int) {
	f.mu.Lock()
	f.statusCode = code
	f.mu.Unlock()
}

func (f *fakeESPNServer) SetFixtures(completed, upcoming []byte) {
	f.mu.Lock()
	f.completedJSON = completed
	f.upcomingJSON = upcoming
	f.mu.Unlock()
}

func (f *fakeESPNServer) Requests() int {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.requests
}

type deliveredAlert struct {
	ChannelID string
	Alert     uccalendar.Alert
}

type fakeAlertSender struct {
	mu        sync.Mutex
	delivered []deliveredAlert
	failNext  int
	failErr   error
	notifyCh  chan struct{}
}

func newFakeAlertSender() *fakeAlertSender {
	return &fakeAlertSender{
		notifyCh: make(chan struct{}, 32),
	}
}

func (a *fakeAlertSender) SendUCAlert(_ context.Context, channelID string, alert uccalendar.Alert) error {
	a.mu.Lock()
	if a.failNext > 0 {
		a.failNext--
		err := a.failErr
		if err == nil {
			err = errors.New("simulated discord rest error for channel " + channelID)
		}
		a.mu.Unlock()

		return err
	}

	a.delivered = append(a.delivered, deliveredAlert{
		ChannelID: channelID,
		Alert:     alert,
	})
	a.mu.Unlock()

	select {
	case a.notifyCh <- struct{}{}:
	default:
	}

	return nil
}

func (a *fakeAlertSender) SetFailNext(n int, err error) {
	a.mu.Lock()
	a.failNext = n
	a.failErr = err
	a.mu.Unlock()
}

func (a *fakeAlertSender) Delivered() []deliveredAlert {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]deliveredAlert, len(a.delivered))
	copy(out, a.delivered)

	return out
}

type fakeBanterGenerator struct {
	mu        sync.Mutex
	calls     int
	prompts   []string
	response  string
	err       error
	startedCh chan struct{}
	gateCh    <-chan struct{}
}

func newFakeBanter(response string) *fakeBanterGenerator {
	return &fakeBanterGenerator{
		response:  response,
		startedCh: make(chan struct{}, 32),
	}
}

func (b *fakeBanterGenerator) Generate(ctx context.Context, prompt string) (string, error) {
	b.mu.Lock()
	b.calls++
	b.prompts = append(b.prompts, prompt)
	gate := b.gateCh
	resp := b.response
	genErr := b.err
	b.mu.Unlock()

	select {
	case b.startedCh <- struct{}{}:
	default:
	}

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", fmt.Errorf("banter canceled: %w", ctx.Err())
		}
	}

	if genErr != nil {
		return "", genErr
	}

	return resp, nil
}

func (b *fakeBanterGenerator) SetResponse(response string, err error) {
	b.mu.Lock()
	b.response = response
	b.err = err
	b.mu.Unlock()
}

func (b *fakeBanterGenerator) SetGate(gate <-chan struct{}) {
	b.mu.Lock()
	b.gateCh = gate
	b.mu.Unlock()
}

func (b *fakeBanterGenerator) Calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.calls
}

func (b *fakeBanterGenerator) Prompts() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]string, len(b.prompts))
	copy(out, b.prompts)

	return out
}

func santiagoLocation(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation(uccalendar.SantiagoTimezone)
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	return loc
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()

	data, err := testdataFS.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}

	return data
}

func newIsolatedDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	sources, err := migrationSources()
	if err != nil {
		t.Fatalf("migrationSources: %v", err)
	}

	isolated, err := pgtest.New(t.Context(), pgtest.Options{Sources: sources})
	if err != nil {
		t.Fatalf("pgtest.New: %v", err)
	}

	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
		defer cancel()

		closeErr := isolated.Close(closeCtx)
		if closeErr != nil {
			t.Errorf("isolated.Close: %v", closeErr)
		}
	})

	return isolated.Pool()
}

func newRealConfigStore(t *testing.T, pool *pgxpool.Pool) *config.Store {
	t.Helper()

	rawKey, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}

	master, err := crypto.ParseMasterKey(rawKey)
	if err != nil {
		t.Fatalf("ParseMasterKey: %v", err)
	}

	keys := []config.Key{
		{
			Name: uccalendar.SettingFriendUserID,
			Validate: func(v string, _ config.Environment) error {
				if snowErr := uccalendar.ValidateSnowflake(v); snowErr != nil {
					return fmt.Errorf("invalid snowflake: %w", snowErr)
				}

				return nil
			},
			Doc: "UC friend user ID",
		},
		{
			Name: uccalendar.SettingChannelID,
			Validate: func(v string, _ config.Environment) error {
				if snowErr := uccalendar.ValidateSnowflake(v); snowErr != nil {
					return fmt.Errorf("invalid snowflake: %w", snowErr)
				}

				return nil
			},
			Doc: "UC alerts channel ID",
		},
		{
			Name:    uccalendar.SettingFridayAlertTime,
			Default: uccalendar.DefaultFridayAlertTime,
			Validate: func(v string, _ config.Environment) error {
				_, _, parseErr := uccalendar.ParseClockHHMM(v)
				if parseErr != nil {
					return fmt.Errorf("invalid clock: %w", parseErr)
				}

				return nil
			},
			Doc: "UC Friday alert time",
		},
		{
			Name:    uccalendar.SettingMatchdayAlertTime,
			Default: uccalendar.DefaultMatchdayAlertTime,
			Validate: func(v string, _ config.Environment) error {
				_, _, parseErr := uccalendar.ParseClockHHMM(v)
				if parseErr != nil {
					return fmt.Errorf("invalid clock: %w", parseErr)
				}

				return nil
			},
			Doc: "UC matchday alert time",
		},
		{
			Name:    uccalendar.SettingSlangCacheTTL,
			Default: "24h",
			Validate: func(v string, _ config.Environment) error {
				_, parseErr := uccalendar.ParseSlangCacheTTL(v)
				if parseErr != nil {
					return fmt.Errorf("invalid ttl: %w", parseErr)
				}

				return nil
			},
			Doc: "UC slang cache TTL",
		},
		{
			Name:    uccalendar.SettingAPIBaseURL,
			Default: uccalendar.DefaultAPIBaseURL,
			Doc:     "UC ESPN API base URL",
		},
	}

	store, err := config.NewStore(t.Context(), pool, master, config.EnvLocal, keys)
	if err != nil {
		t.Fatalf("config.NewStore: %v", err)
	}

	return store
}

func waitForCondition(t *testing.T, desc string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		runtime.Gosched()
	}

	t.Fatalf("timed out waiting for %s", desc)
}

func cleanSentAlerts(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	_, err := pool.Exec(t.Context(), "DELETE FROM uccalendar.sent_alert")
	if err != nil {
		t.Fatalf("clean uccalendar.sent_alert: %v", err)
	}
}

var _ db.Source = (*db.GooseSource)(nil)
