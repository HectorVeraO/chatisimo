package discord_test

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/disgoorg/disgo/discord"

	appdiscord "github.com/HectorVeraO/chatisimo/internal/discord"
	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
)

// Fixed fake snowflakes.
const (
	userA   = "100000000000000001"
	userB   = "100000000000000002"
	userC   = "100000000000000003"
	userGap = "100000000000000009"
	guildID = "200000000000000001"

	guildName    = "Test server"
	exchangeName = "Winter exchange"

	deliveryDeadline = 30 * time.Second
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

type lockedRandom struct {
	mu  sync.Mutex
	rng *rand.Rand
}

func (r *lockedRandom) IntN(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.rng.IntN(n)
}

func (r *lockedRandom) Shuffle(n int, swap func(i, j int)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.rng.Shuffle(n, swap)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log buffer: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// world is a fake Discord with three users in one guild, and a Notifier pointed at it.
type world struct {
	fake     *fakediscord.Server
	notifier *appdiscord.Notifier
	logs     *syncBuffer
	logger   *slog.Logger
	users    map[string]fakediscord.User
}

func newWorld(t *testing.T) *world {
	t.Helper()

	fake, err := fakediscord.New(fakediscord.Options{})
	if err != nil {
		t.Fatalf("fakediscord.New: %v", err)
	}
	startErr := fake.Start(t.Context())
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() {
		closeErr := fake.Close()
		if closeErr != nil {
			t.Errorf("Close fake: %v", closeErr)
		}
	})

	users := map[string]fakediscord.User{}
	var members []fakediscord.User
	for i, id := range []string{userA, userB, userC} {
		user := fake.AddUser(fakediscord.User{ID: id, Name: fmt.Sprintf("tester-%d", i)})
		users[id] = user
		members = append(members, user)
	}
	fake.AddGuild(fakediscord.Guild{ID: guildID, Name: guildName}, members...)

	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	notifier, err := appdiscord.NewNotifier(appdiscord.Settings{
		Token:      config.NewSecret(fake.Token()),
		APIBaseURL: fake.RESTBase(),
	}, logger)
	if err != nil {
		t.Fatalf("NewNotifier: %v", err)
	}

	t.Cleanup(func() { notifier.Close(context.Background()) })

	return &world{fake: fake, notifier: notifier, logs: logs, logger: logger, users: users}
}

// cancelledNotice is a notification that needs no counterpart, addressed to subject.
func cancelledNotice(subject string) giftexchange.Notification {
	n := giftexchange.Notification{
		ID:   giftexchange.NotificationID(uuid.NewV7()),
		Kind: giftexchange.KindCancelled,
		To: giftexchange.PersonRef{Identities: []giftexchange.ExternalIdentity{
			{Provider: giftexchange.ProviderDiscord, Subject: subject},
		}},
		Exchange: giftexchange.ExchangeSummary{
			Name:  exchangeName,
			Group: giftexchange.Group{Provider: giftexchange.ProviderDiscord, Subject: guildID},
		},
	}
	return n
}

// service is a giftexchange.Service over the shared database that delivers through the
// world's notifier, with its own River queue.
type service struct {
	svc    *giftexchange.Service
	clock  *fakeClock
	queue  string
	people map[string]person
	group  giftexchange.GroupID
}

type person struct {
	actor giftexchange.Actor
	id    giftexchange.PersonID
}

func newService(t *testing.T, w *world) *service {
	t.Helper()

	clock := &fakeClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	random := &lockedRandom{rng: rand.New(rand.NewPCG(1, 2))} //nolint:gosec // seeded test randomness

	var raw [8]byte
	_, readErr := cryptorand.Read(raw[:])
	if readErr != nil {
		t.Fatalf("read randomness: %v", readErr)
	}
	queue := "t_" + hex.EncodeToString(raw[:])

	scheduler, err := jobs.NewScheduler(shared.Pool(), jobs.SchedulerOptions{Logger: w.logger, Clock: clock, Queue: queue})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	svc, err := giftexchange.New(giftexchange.Deps{
		Pool:      shared.Pool(),
		Scheduler: scheduler,
		Clock:     clock,
		Random:    random,
		Notifier:  w.notifier,
		Logger:    w.logger,
	})
	if err != nil {
		t.Fatalf("giftexchange.New: %v", err)
	}

	groupRef := giftexchange.GroupRef{Provider: giftexchange.ProviderDiscord, Subject: guildID}
	group, err := svc.ResolveGroup(t.Context(), groupRef)
	if err != nil {
		t.Fatalf("ResolveGroup: %v", err)
	}

	s := &service{svc: svc, clock: clock, queue: queue, people: map[string]person{}, group: group}
	for _, subject := range []string{userA, userB, userC} {
		ref := giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: subject}
		actor, resolveErr := svc.ResolveActor(t.Context(), ref)
		if resolveErr != nil {
			t.Fatalf("ResolveActor: %v", resolveErr)
		}
		s.people[subject] = person{actor: actor, id: actor.Person()}
	}

	return s
}

// drawThree creates an exchange organized by A, joined by A, B and C, and draws it.
func (s *service) drawThree(t *testing.T) giftexchange.ExchangeID {
	t.Helper()

	organizer := s.people[userA]
	view, err := s.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: s.group,
		Name:  exchangeName,
	})
	if err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}
	for _, subject := range []string{userA, userB, userC} {
		joinErr := s.svc.Join(t.Context(), s.people[subject].actor, s.group, view.ID)
		if joinErr != nil {
			t.Fatalf("Join: %v", joinErr)
		}
	}
	drawErr := s.svc.Draw(t.Context(), organizer.actor, s.group, view.ID)
	if drawErr != nil {
		t.Fatalf("Draw: %v", drawErr)
	}

	return view.ID
}

func (s *service) startRunner(t *testing.T, w *world) {
	t.Helper()

	registry := jobs.NewRegistry()
	registerErr := s.svc.RegisterJobs(registry)
	if registerErr != nil {
		t.Fatalf("RegisterJobs: %v", registerErr)
	}

	runner, err := jobs.NewRunner(shared.Pool(), registry, jobs.RunnerOptions{
		Logger:            w.logger,
		Clock:             s.clock,
		FetchPollInterval: 50 * time.Millisecond,
		Queue:             s.queue,
		TestOnly:          true,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	startErr := runner.Start(context.Background())
	if startErr != nil {
		t.Fatalf("Runner.Start: %v", startErr)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
		defer cancel()

		stopErr := runner.Stop(stopCtx)
		if stopErr != nil {
			t.Errorf("Runner.Stop: %v", stopErr)
		}
	})
}

// finishedCount counts the notices of the exchange that ended with outcome.
func finishedCount(t *testing.T, x giftexchange.ExchangeID, outcome string) int {
	t.Helper()

	var count int
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM giftexchange.notifications WHERE exchange_id = $1 AND outcome = $2",
		x.String(), outcome).Scan(&count)
	if err != nil {
		t.Fatalf("count notices: %v", err)
	}

	return count
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	end := time.Now().Add(deliveryDeadline)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// embedOf returns the title and description of the one embed of a fake DM.
func embedOf(t *testing.T, m fakediscord.Message) (string, string) {
	t.Helper()

	if len(m.Embeds) != 1 {
		t.Fatalf("message has %d embeds, want 1", len(m.Embeds))
	}
	var embed discord.Embed
	err := json.Unmarshal(m.Embeds[0], &embed)
	if err != nil {
		t.Fatalf("decode embed: %v", err)
	}

	return embed.Title, embed.Description
}

// firstFound returns the first needle that text contains.
func firstFound(text string, needles ...string) (string, bool) {
	for _, needle := range needles {
		if needle != "" && strings.Contains(text, needle) {
			return needle, true
		}
	}

	return "", false
}
