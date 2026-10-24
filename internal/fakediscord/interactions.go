package fakediscord

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Interaction kinds, as the control API names them.
const (
	kindSlash        = "slash"
	kindAutocomplete = "autocomplete"
	kindButton       = "button"
	kindSelect       = "select"
	kindModal        = "modal"
)

// Interaction callback types.
const (
	callbackMessage         = 4
	callbackDeferMessage    = 5
	callbackDeferUpdate     = 6
	callbackUpdateMessage   = 7
	callbackAutocomplete    = 8
	callbackModal           = 9
	messageFlagEphemeral    = 64
	interactionTokenBytes   = 24
	followupWindow          = 15 * time.Minute
	maxAutocompleteChoices  = 25
	maxModalCustomIDLength  = 100
	maxModalTitleLength     = 45
	interactionExpiredText  = "Unknown interaction"
	alreadyAcknowledgedText = "Interaction has already been acknowledged."
)

// Result statuses.
const (
	StatusResponded = "responded"
	StatusDeferred  = "deferred"
	StatusTimeout   = "timeout"
	StatusRejected  = "rejected"
)

var (
	// ErrRejected is returned when the fake refuses to send an interaction because a
	// Discord client could not have produced it: a missing required option, a custom ID
	// the message does not carry, and the like. The error text lists every problem.
	ErrRejected = errors.New("fakediscord: interaction rejected")
	// ErrNoBot is returned when an interaction has nobody to go to because no bot has
	// identified.
	ErrNoBot = errors.New("fakediscord: no bot session")
	// ErrNoCallback is returned by Autocomplete when the bot did not answer in time.
	ErrNoCallback = errors.New("fakediscord: the bot did not answer the interaction in time")
)

// Choice is one autocomplete suggestion the bot returned.
type Choice struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// Result is what the bot answered to an interaction. Status tells how: responded (a message,
// choices or a modal came back), deferred, or timeout when no callback arrived by the
// deadline.
type Result struct {
	InteractionID string
	Status        string
	CallbackType  int
	// Message is the message the callback created or updated, if it did.
	Message *Message
	// Choices holds the autocomplete suggestions of a type 8 callback.
	Choices []Choice
	// Modal is the modal a type 9 callback opened.
	Modal *Modal
}

type interactionState int

const (
	statePending interactionState = iota
	stateAnswered
	stateExpired
)

// interaction is one INTERACTION_CREATE the fake sent, tracked by its token.
type interaction struct {
	id              string
	token           string
	kind            string
	actorID         string
	channelID       string
	sourceMessageID string
	deadline        time.Time
	state           interactionState
	callbackType    int
	answeredAt      time.Time
	// ephemeral is whether the response the callback opened is visible to the actor only.
	ephemeral bool
	// originalID is the message @original names, empty until there is one.
	originalID string
	// originalDeleted is whether the bot deleted the original, which ends the placeholder too.
	originalDeleted bool
	// followupIDs are the other messages created through the interaction's webhook.
	followupIDs []string
	answered    chan struct{}
	result      Result
}

// plan is a validated interaction ready to send.
type plan struct {
	kind            string
	template        string
	data            templateData
	actorID         string
	channelID       string
	sourceMessageID string
	// consumeModalFor is the user whose pending modal this submission closes.
	consumeModalFor string
}

// newInteractionToken returns 32 random base64url characters. They share nothing with the
// bot token's shape.
func newInteractionToken() (string, error) {
	buf := make([]byte, interactionTokenBytes)
	_, err := rand.Read(buf)
	if err != nil {
		return "", fmt.Errorf("interaction token: read random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// isLive reports whether the interaction's token can still be used at now, and records the
// expiry when it cannot.
func (i *interaction) isLive(now time.Time) bool {
	switch i.state {
	case statePending:
		if now.After(i.deadline) {
			i.state = stateExpired
			return false
		}
		return true
	case stateAnswered:
		if now.After(i.answeredAt.Add(followupWindow)) {
			i.state = stateExpired
			return false
		}
		return true
	default:
		return false
	}
}

// awaitsOriginal reports whether a deferred response still shows its placeholder, so the
// first edit or followup becomes the original.
func (i *interaction) awaitsOriginal() bool {
	return i.callbackType == callbackDeferMessage && i.originalID == "" && !i.originalDeleted
}

// ownsMessage reports whether the message was created or adopted by this interaction.
func (i *interaction) ownsMessage(id string) bool {
	if id == "" {
		return false
	}
	if id == i.originalID {
		return true
	}
	for _, followup := range i.followupIDs {
		if followup == id {
			return true
		}
	}

	return false
}

// act builds a plan under the lock, sends it to the bot and waits for the first callback.
func (s *Server) act(ctx context.Context, build func() (*plan, error)) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("fakediscord: nil context")
	}

	s.mu.Lock()
	launched, err := s.launchLocked(build)
	s.mu.Unlock()
	if err != nil {
		return Result{}, err
	}

	return s.await(ctx, launched)
}

// launchLocked builds the plan, fills its template and dispatches INTERACTION_CREATE to the
// newest bot session. Callers hold s.mu.
func (s *Server) launchLocked(build func() (*plan, error)) (*interaction, error) {
	if s.closed {
		return nil, ErrClosed
	}

	p, err := build()
	if err != nil {
		return nil, err
	}

	sess := s.newestSessionLocked()
	if sess == nil {
		return nil, ErrNoBot
	}

	token, err := newInteractionToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	intr := &interaction{
		id:              s.ids.next(),
		token:           token,
		kind:            p.kind,
		actorID:         p.actorID,
		channelID:       p.channelID,
		sourceMessageID: p.sourceMessageID,
		deadline:        now.Add(s.callbackDeadline),
		answered:        make(chan struct{}),
	}
	p.data.InteractionID = intr.id
	p.data.Token = intr.token
	p.data.AppID = s.appID

	payload, err := fillTemplate(s.templates, p.template, p.data)
	if err != nil {
		panic("fakediscord: " + err.Error())
	}

	s.purgeInteractionsLocked(now)
	s.w.interactions[intr.token] = intr
	s.dispatchLocked(sess, "INTERACTION_CREATE", json.RawMessage(payload))
	if p.consumeModalFor != "" {
		delete(s.w.modals, p.consumeModalFor)
	}
	s.logger.Debug("interaction sent", "kind", p.kind)

	return intr, nil
}

// purgeInteractionsLocked forgets interactions whose tokens can no longer be used.
func (s *Server) purgeInteractionsLocked(now time.Time) {
	for token, intr := range s.w.interactions {
		if !intr.isLive(now) {
			delete(s.w.interactions, token)
		}
	}
}

// newestSessionLocked returns the most recently identified bot session, or nil.
func (s *Server) newestSessionLocked() *session {
	var newest *session
	for _, sess := range s.sessions {
		if newest == nil || sess.order > newest.order {
			newest = sess
		}
	}

	return newest
}

// await waits for the interaction's first callback, its deadline, ctx or Close.
func (s *Server) await(ctx context.Context, intr *interaction) (Result, error) {
	timer := time.NewTimer(time.Until(intr.deadline))
	defer timer.Stop()

	select {
	case <-intr.answered:
	case <-timer.C:
	case <-ctx.Done():
		return Result{}, fmt.Errorf("wait for the bot to answer the interaction: %w", ctx.Err())
	case <-s.stopped:
		return Result{}, ErrClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if intr.callbackType == 0 {
		intr.state = stateExpired
		return Result{InteractionID: intr.id, Status: StatusTimeout}, nil
	}

	return cloneResult(intr.result), nil
}

func cloneResult(r Result) Result {
	if r.Message != nil {
		copied := cloneMessage(*r.Message)
		r.Message = &copied
	}
	if r.Modal != nil {
		copied := *r.Modal
		copied.Components = append([]json.RawMessage(nil), copied.Components...)
		r.Modal = &copied
	}
	r.Choices = append([]Choice(nil), r.Choices...)

	return r
}
