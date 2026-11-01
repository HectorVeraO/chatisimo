package fakediscord

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	codeUnknownMessage     = 10008
	codeUnknownInteraction = 10062
	codeAlreadyAcked       = 40060
	codeCannotDM           = 50007
	maxNonceLength         = 25
	nonceWindow            = 5 * time.Minute
)

// outcome is what a handler decided to answer, built while holding the lock and written
// after releasing it.
type outcome struct {
	status  int
	isError bool
	code    int
	message string
	value   any
}

func succeed(status int, value any) outcome {
	return outcome{status: status, value: value}
}

func fail(status, code int, message string) outcome {
	return outcome{status: status, isError: true, code: code, message: message}
}

func (o outcome) write(w http.ResponseWriter) {
	switch {
	case o.isError:
		writeError(w, o.status, o.code, o.message)
	case o.value == nil:
		w.WriteHeader(o.status)
	default:
		writeJSON(w, o.status, o.value)
	}
}

// messageUpdateBody is the body of an edit: a field left out stays as it is, and a null
// clears it.
type messageUpdateBody struct {
	Content         json.RawMessage `json:"content"`
	Embeds          json.RawMessage `json:"embeds"`
	Components      json.RawMessage `json:"components"`
	AllowedMentions json.RawMessage `json:"allowed_mentions"`
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// applyTo returns m with the update applied, and every problem found.
func (u messageUpdateBody) applyTo(m Message) (Message, []error) {
	var problems []error

	if len(u.Content) > 0 {
		m.Content = ""
		if !isNull(u.Content) {
			err := json.Unmarshal(u.Content, &m.Content)
			if err != nil {
				problems = append(problems, errors.New("content: must be a string"))
			}
		}
	}
	m.Embeds = decodeRawList(u.Embeds, m.Embeds, "embeds", &problems)
	m.Components = decodeRawList(u.Components, m.Components, "components", &problems)

	hasNothing := m.Content == "" && len(m.Embeds) == 0 && len(m.Components) == 0
	if hasNothing {
		problems = append(problems, errEmptyMessage)
	}

	return m, problems
}

var errEmptyMessage = errors.New("message: cannot send an empty message")

func decodeRawList(raw json.RawMessage, current []json.RawMessage, field string, problems *[]error) []json.RawMessage {
	if len(raw) == 0 {
		return current
	}
	if isNull(raw) {
		return []json.RawMessage{}
	}

	var list []json.RawMessage
	err := json.Unmarshal(raw, &list)
	if err != nil {
		*problems = append(*problems, fmt.Errorf("%s: must be an array", field))
		return current
	}

	return nonNilRaw(list)
}

// problemsOutcome answers with every problem found in a request. A message that is only
// empty keeps Discord's own code; anything else is a form-body error.
func problemsOutcome(problems []error) outcome {
	onlyEmpty := len(problems) == 1 && errors.Is(problems[0], errEmptyMessage)
	if onlyEmpty {
		return fail(http.StatusBadRequest, codeEmptyMessage, "Cannot send an empty message")
	}

	joined := strings.ReplaceAll(errors.Join(problems...).Error(), "\n", "; ")

	return fail(http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: "+joined)
}

// checkMessageBody validates a message to create and returns its allowed_mentions as sent.
func checkMessageBody(body createMessageBody) (*AllowedMentions, *outcome) {
	sent, mentionsErr := parseAllowedMentions(body.AllowedMentions)

	var problems []error
	if mentionsErr != nil {
		problems = append(problems, errors.New("allowed_mentions: invalid"))
	}
	hasNothing := body.Content == "" && len(body.Embeds) == 0 && len(body.Components) == 0
	if hasNothing {
		problems = append(problems, errEmptyMessage)
	}
	if len(nonceText(body.Nonce)) > maxNonceLength {
		problems = append(problems, fmt.Errorf("nonce: must be %d or fewer in length", maxNonceLength))
	}
	if len(problems) == 0 {
		return sent, nil
	}

	failure := problemsOutcome(problems)

	return nil, &failure
}

// nonceText is the nonce as a string: a JSON string unquoted, a number as written.
func nonceText(raw json.RawMessage) string {
	if len(raw) == 0 || isNull(raw) {
		return ""
	}

	var text string
	err := json.Unmarshal(raw, &text)
	if err == nil {
		return text
	}

	return string(bytes.TrimSpace(raw))
}

// storeMessageLocked stores a new message, records the request that made it and tells event
// subscribers. Callers hold s.mu.
func (s *Server) storeMessageLocked(m Message, in recordInput) Message {
	s.w.messages = append(s.w.messages, m)

	in.message = m
	in.status = http.StatusOK
	in.visibleTo = s.w.visibleTo(m)
	in.guildID = s.guildOfChannelLocked(m.ChannelID)
	s.appendRecordLocked(in)

	s.emitMessageLocked(eventMessageCreated, m)
	s.notifyLocked()

	return cloneMessage(m)
}

// editMessageLocked replaces the stored message at index with updated and records the
// request. Callers hold s.mu.
func (s *Server) editMessageLocked(index int, updated Message, in recordInput) Message {
	updated.EditedAt = time.Now().UTC()
	s.w.messages[index] = updated

	in.message = updated
	in.status = http.StatusOK
	in.visibleTo = s.w.visibleTo(updated)
	in.guildID = s.guildOfChannelLocked(updated.ChannelID)
	s.appendRecordLocked(in)

	s.emitMessageLocked(eventMessageUpdated, updated)
	s.notifyLocked()

	return cloneMessage(updated)
}

// deleteMessageLocked removes the message at index and tells event subscribers.
func (s *Server) deleteMessageLocked(index int) {
	removed := s.w.messages[index]
	s.w.messages = append(s.w.messages[:index], s.w.messages[index+1:]...)
	s.emitMessageLocked(eventMessageDeleted, removed)
	s.notifyLocked()
}

// recordRejectedLocked records a message request the fake refused, for a message that was
// never stored.
func (s *Server) recordRejectedLocked(m Message, status int, in recordInput) {
	in.message = m
	in.status = status
	in.visibleTo = s.w.visibleTo(m)
	in.guildID = s.guildOfChannelLocked(m.ChannelID)
	s.appendRecordLocked(in)
}

// guildOfChannelLocked returns the guild a channel belongs to, or empty for a DM.
func (s *Server) guildOfChannelLocked(channelID string) string {
	channel, found := s.w.channels[channelID]
	if !found {
		return ""
	}

	return channel.guildID
}

// newMessageLocked builds a message from the bot in the channel.
func (s *Server) newMessageLocked(channelID string, body createMessageBody) Message {
	var replyToID string
	if body.MessageReference != nil {
		replyToID = body.MessageReference.MessageID
	}

	return Message{
		ID:         s.ids.next(),
		ChannelID:  channelID,
		AuthorID:   s.appID,
		Content:    body.Content,
		Embeds:     nonNilRaw(body.Embeds),
		Components: nonNilRaw(body.Components),
		Flags:      body.Flags,
		CreatedAt:  time.Now().UTC(),
		ReplyToID:  replyToID,
	}
}

// dedupeKey identifies a nonce within a channel.
func dedupeKey(channelID, nonce string) string {
	return channelID + "\x00" + nonce
}
