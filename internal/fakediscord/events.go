package fakediscord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Event names of the control API's event stream.
const (
	eventMessageCreated = "message_created"
	eventMessageUpdated = "message_updated"
	eventMessageDeleted = "message_deleted"
	eventModalOpened    = "modal_opened"
	eventWorldChanged   = "world_changed"
	eventBotStatus      = "bot_status"

	botAbsent     = "absent"
	botConnecting = "connecting"
	botReady      = "ready"

	subscriberBuffer = 256
	keepAliveEvery   = 15 * time.Second
)

// subscriber is one event stream. Its channel is closed when the stream must end.
type subscriber struct {
	// as is the user whose view the stream shows, or empty for every event.
	as string
	ch chan sseEvent
}

// sseEvent is one event and the audience it is meant for.
type sseEvent struct {
	name string
	data []byte
	// userID limits the event to one user when set.
	userID string
	// message limits the event to those who can see it when set.
	message *Message
}

// subscribeLocked registers a stream for the user's view. It returns nil when the server is
// closed.
func (s *Server) subscribeLocked(as string) *subscriber {
	if s.closed {
		return nil
	}
	sub := &subscriber{as: as, ch: make(chan sseEvent, subscriberBuffer)}
	s.subs[sub] = struct{}{}

	return sub
}

func (s *Server) unsubscribe(sub *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dropSubscriberLocked(sub)
}

func (s *Server) dropSubscriberLocked(sub *subscriber) {
	_, isLive := s.subs[sub]
	if !isLive {
		return
	}
	delete(s.subs, sub)
	close(sub.ch)
}

// publishLocked offers the event to every stream it is meant for. A stream that cannot keep
// up is ended, and its client reconnects.
func (s *Server) publishLocked(event sseEvent) {
	for sub := range s.subs {
		if !s.isEventForLocked(sub, event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			s.dropSubscriberLocked(sub)
		}
	}
}

func (s *Server) isEventForLocked(sub *subscriber, event sseEvent) bool {
	if sub.as == "" {
		return true
	}
	if event.userID != "" {
		return event.userID == sub.as
	}
	if event.message != nil {
		return s.w.isMessageVisible(sub.as, *event.message)
	}

	return true
}

func mustMarshalEvent(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic("fakediscord: event is not marshalable: " + err.Error())
	}

	return data
}

// emitMessageLocked publishes a message event to the users who can see the message.
func (s *Server) emitMessageLocked(name string, m Message) {
	payload := map[string]any{"message": messageToJSON(m, s.botUser())}
	guildID := s.guildOfChannelLocked(m.ChannelID)
	if guildID != "" {
		payload["guild_id"] = guildID
	}
	if name == eventMessageDeleted {
		payload = map[string]any{"id": m.ID, "channel_id": m.ChannelID}
	}

	copied := cloneMessage(m)
	s.publishLocked(sseEvent{name: name, data: mustMarshalEvent(payload), message: &copied})
}

// emitModalLocked publishes modal_opened to the user the modal is for.
func (s *Server) emitModalLocked(m Modal) {
	s.publishLocked(sseEvent{name: eventModalOpened, data: mustMarshalEvent(modalToJSON(m)), userID: m.UserID})
}

func (s *Server) publishWorldChangedLocked() {
	s.publishLocked(sseEvent{name: eventWorldChanged, data: []byte("{}")})
}

// botStatusLocked reports whether a bot is absent, connecting or ready.
func (s *Server) botStatusLocked() string {
	switch {
	case s.readyLocked():
		return botReady
	case len(s.sessions) > 0 || len(s.conns) > 0:
		return botConnecting
	default:
		return botAbsent
	}
}

// publishBotStatusLocked tells the streams when the bot's status changed.
func (s *Server) publishBotStatusLocked() {
	status := s.botStatusLocked()
	if status == s.botStatus {
		return
	}
	s.botStatus = status
	s.publishLocked(sseEvent{name: eventBotStatus, data: mustMarshalEvent(map[string]string{"bot": status})})
}

// handleEvents streams the control API's events as server-sent events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	as := r.URL.Query().Get("as")

	s.mu.Lock()
	_, isKnown := s.w.users[as]
	if as != "" && !isKnown {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, codeGeneric, "Unknown user")
		return
	}
	sub := s.subscribeLocked(as)
	s.mu.Unlock()

	if sub == nil {
		writeError(w, http.StatusServiceUnavailable, codeGeneric, "server closed")
		return
	}
	defer s.unsubscribe(sub)

	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	// A failed write means the client is gone; the Flush below reports it.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flushErr := controller.Flush()
	if flushErr != nil {
		return
	}

	keepAlive := time.NewTicker(keepAliveEvery)
	defer keepAlive.Stop()
	for {
		select {
		case event, isOpen := <-sub.ch:
			if !isOpen {
				return
			}
			_, writeErr := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, event.data)
			if writeErr != nil || controller.Flush() != nil {
				return
			}
		case <-keepAlive.C:
			_, writeErr := fmt.Fprint(w, ": keep-alive\n\n")
			if writeErr != nil || controller.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		case <-s.stopped:
			return
		}
	}
}
