package fakediscord

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
)

const (
	originalRef          = "@original"
	maxModalComponents   = 5
	invalidResponseText  = "Invalid interaction response type"
	unknownMessageText   = "Unknown Message"
	unknownApplicationFn = "Unknown Application"
)

// callbackBody is the body of POST /interactions/{id}/{token}/callback.
type callbackBody struct {
	Type int             `json:"type"`
	Data json.RawMessage `json:"data"`
}

// allowedCallbacks lists, per interaction kind, the callback types Discord accepts.
func allowedCallbacks(intr *interaction) []int {
	switch intr.kind {
	case kindSlash:
		return []int{callbackMessage, callbackDeferMessage, callbackModal}
	case kindAutocomplete:
		return []int{callbackAutocomplete}
	case kindButton, kindSelect:
		return []int{callbackMessage, callbackDeferMessage, callbackDeferUpdate, callbackUpdateMessage, callbackModal}
	case kindModal:
		if intr.sourceMessageID != "" {
			return []int{callbackMessage, callbackDeferMessage, callbackDeferUpdate, callbackUpdateMessage}
		}
		return []int{callbackMessage, callbackDeferMessage}
	default:
		return nil
	}
}

func (s *Server) addWebhookRoutes(mux *http.ServeMux) {
	base := restPrefix + "/webhooks/{app}/{token}"
	mux.HandleFunc("POST "+restPrefix+"/interactions/{id}/{token}/callback", s.faulted(routeCallback, s.handleCallback))
	mux.HandleFunc("PATCH "+base+"/messages/"+originalRef, s.faulted(routeEditOriginal, s.handleEditOriginal))
	mux.HandleFunc("GET "+base+"/messages/"+originalRef, s.faulted(routeGetOriginal, s.handleGetOriginal))
	mux.HandleFunc("DELETE "+base+"/messages/"+originalRef, s.faulted(routeDeleteOriginal, s.handleDeleteOriginal))
	mux.HandleFunc("POST "+base, s.faulted(routeFollowup, s.handleFollowup))
	mux.HandleFunc("PATCH "+base+"/messages/{message}", s.faulted(routeEditMessage, s.handleEditMessage))
	mux.HandleFunc("GET "+base+"/messages/{message}", s.faulted(routeGetMessage, s.handleGetMessage))
	mux.HandleFunc("DELETE "+base+"/messages/{message}", s.faulted(routeDeleteMessage, s.handleDeleteMessage))
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	var body callbackBody
	if !decodeBody(w, r, &body) {
		return
	}

	s.mu.Lock()
	out := s.applyCallbackLocked(r.PathValue("id"), r.PathValue("token"), body)
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) applyCallbackLocked(id, token string, body callbackBody) outcome {
	intr, found := s.w.interactions[token]
	if !found || intr.id != id {
		return fail(http.StatusNotFound, codeUnknownInteraction, interactionExpiredText)
	}

	if !intr.isLive(time.Now()) {
		s.recordRejectedCallbackLocked(intr, body, http.StatusNotFound)
		return fail(http.StatusNotFound, codeUnknownInteraction, interactionExpiredText)
	}
	if intr.state == stateAnswered {
		s.recordRejectedCallbackLocked(intr, body, http.StatusBadRequest)
		return fail(http.StatusBadRequest, codeAlreadyAcked, alreadyAcknowledgedText)
	}
	if !slices.Contains(allowedCallbacks(intr), body.Type) {
		return fail(http.StatusBadRequest, codeInvalidFormBody, invalidResponseText)
	}

	switch body.Type {
	case callbackMessage:
		return s.callbackMessageLocked(intr, body.Data)
	case callbackDeferMessage:
		return s.callbackDeferLocked(intr, body.Data)
	case callbackDeferUpdate:
		intr.originalID = intr.sourceMessageID
		s.answerLocked(intr, callbackDeferUpdate, Result{})
		return succeed(http.StatusNoContent, nil)
	case callbackUpdateMessage:
		return s.callbackUpdateLocked(intr, body.Data)
	case callbackAutocomplete:
		return s.callbackAutocompleteLocked(intr, body.Data)
	case callbackModal:
		return s.callbackModalLocked(intr, body.Data)
	default:
		panic(fmt.Sprintf("fakediscord: callback type %d passed validation but has no handler", body.Type))
	}
}

// recordRejectedCallbackLocked records a refused callback that carried a message, so tests
// can see what the bot tried to say.
func (s *Server) recordRejectedCallbackLocked(intr *interaction, body callbackBody, status int) {
	isMessage := body.Type == callbackMessage || body.Type == callbackUpdateMessage
	channel, found := s.w.channels[intr.channelID]
	if !isMessage || !found {
		return
	}

	var data createMessageBody
	err := json.Unmarshal(body.Data, &data)
	if err != nil {
		return
	}
	// A malformed allowed_mentions records as not sent; the callback is refused either way.
	sent, _ := parseAllowedMentions(data.AllowedMentions)

	attempt := s.newMessageLocked(channel.id, data)
	attempt.ID = ""
	if data.Flags&messageFlagEphemeral != 0 {
		attempt.EphemeralFor = intr.actorID
	}
	s.recordRejectedLocked(attempt, status, recordInput{
		route:         routeCallback,
		source:        sourceCallback,
		interactionID: intr.id,
		sentContent:   data.Content,
		sent:          sent,
	})
}

// answerLocked marks the interaction answered and wakes the action waiting on it.
func (s *Server) answerLocked(intr *interaction, callbackType int, result Result) {
	result.InteractionID = intr.id
	result.CallbackType = callbackType
	result.Status = StatusResponded
	if callbackType == callbackDeferMessage || callbackType == callbackDeferUpdate {
		result.Status = StatusDeferred
	}

	intr.state = stateAnswered
	intr.callbackType = callbackType
	intr.answeredAt = time.Now()
	intr.result = result
	close(intr.answered)
}

func (s *Server) callbackMessageLocked(intr *interaction, raw json.RawMessage) outcome {
	var data createMessageBody
	err := json.Unmarshal(raw, &data)
	if err != nil {
		return fail(http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: data: must be a message")
	}
	sent, problem := checkMessageBody(data)
	if problem != nil {
		return *problem
	}
	channel, found := s.w.channels[intr.channelID]
	if !found {
		return fail(http.StatusNotFound, codeUnknownChannel, "Unknown Channel")
	}

	message := s.newMessageLocked(channel.id, data)
	message.InteractionID = intr.id
	isEphemeral := data.Flags&messageFlagEphemeral != 0
	if isEphemeral {
		message.EphemeralFor = intr.actorID
	}
	stored := s.storeMessageLocked(message, recordInput{
		route:         routeCallback,
		source:        sourceCallback,
		interactionID: intr.id,
		sentContent:   data.Content,
		sent:          sent,
	})

	intr.ephemeral = isEphemeral
	intr.originalID = stored.ID
	s.answerLocked(intr, callbackMessage, Result{Message: &stored})

	return succeed(http.StatusNoContent, nil)
}

func (s *Server) callbackDeferLocked(intr *interaction, raw json.RawMessage) outcome {
	var data struct {
		Flags int `json:"flags"`
	}
	hasData := len(bytes.TrimSpace(raw)) > 0 && !isNull(raw)
	if hasData {
		err := json.Unmarshal(raw, &data)
		if err != nil {
			return fail(http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: data: must be an object")
		}
	}

	intr.ephemeral = data.Flags&messageFlagEphemeral != 0
	s.answerLocked(intr, callbackDeferMessage, Result{})

	return succeed(http.StatusNoContent, nil)
}

func (s *Server) callbackUpdateLocked(intr *interaction, raw json.RawMessage) outcome {
	index := s.w.messageIndex(intr.sourceMessageID)
	if index < 0 {
		return fail(http.StatusNotFound, codeUnknownMessage, unknownMessageText)
	}

	var update messageUpdateBody
	err := json.Unmarshal(raw, &update)
	if err != nil {
		return fail(http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: data: must be a message")
	}
	sent, mentionsErr := parseAllowedMentions(update.AllowedMentions)
	updated, problems := update.applyTo(s.w.messages[index])
	if mentionsErr != nil {
		problems = append(problems, errors.New("allowed_mentions: invalid"))
	}
	if len(problems) > 0 {
		return problemsOutcome(problems)
	}

	var sentContent string
	if len(update.Content) > 0 {
		sentContent = updated.Content
	}
	stored := s.editMessageLocked(index, updated, recordInput{
		route:         routeCallback,
		source:        sourceCallback,
		interactionID: intr.id,
		sentContent:   sentContent,
		sent:          sent,
	})

	intr.originalID = stored.ID
	s.answerLocked(intr, callbackUpdateMessage, Result{Message: &stored})

	return succeed(http.StatusNoContent, nil)
}

func (s *Server) callbackAutocompleteLocked(intr *interaction, raw json.RawMessage) outcome {
	var data struct {
		Choices []Choice `json:"choices"`
	}
	err := json.Unmarshal(raw, &data)
	if err != nil {
		return fail(http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: data.choices: must be an array of choices")
	}

	var problems []error
	if len(data.Choices) > maxAutocompleteChoices {
		problems = append(problems, fmt.Errorf("data.choices: at most %d choices", maxAutocompleteChoices))
	}
	for index, choice := range data.Choices {
		if choice.Name == "" {
			problems = append(problems, fmt.Errorf("data.choices[%d].name: required", index))
		}
		if choice.Value == nil {
			problems = append(problems, fmt.Errorf("data.choices[%d].value: required", index))
		}
	}
	if len(problems) > 0 {
		return problemsOutcome(problems)
	}

	choices := data.Choices
	if choices == nil {
		choices = []Choice{}
	}
	s.answerLocked(intr, callbackAutocomplete, Result{Choices: choices})

	return succeed(http.StatusNoContent, nil)
}

func (s *Server) callbackModalLocked(intr *interaction, raw json.RawMessage) outcome {
	var data struct {
		CustomID   string            `json:"custom_id"`
		Title      string            `json:"title"`
		Components []json.RawMessage `json:"components"`
	}
	err := json.Unmarshal(raw, &data)
	if err != nil {
		return fail(http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: data: must be a modal")
	}

	var problems []error
	if data.CustomID == "" || len(data.CustomID) > maxModalCustomIDLength {
		problems = append(problems, fmt.Errorf("data.custom_id: must be 1-%d characters", maxModalCustomIDLength))
	}
	if data.Title == "" || len([]rune(data.Title)) > maxModalTitleLength {
		problems = append(problems, fmt.Errorf("data.title: must be 1-%d characters", maxModalTitleLength))
	}
	if len(data.Components) == 0 || len(data.Components) > maxModalComponents {
		problems = append(problems, fmt.Errorf("data.components: must hold 1-%d components", maxModalComponents))
	}
	if len(problems) > 0 {
		return problemsOutcome(problems)
	}

	modal := Modal{
		UserID:     intr.actorID,
		CustomID:   data.CustomID,
		Title:      data.Title,
		Components: data.Components,
	}
	s.w.modals[intr.actorID] = &pendingModal{Modal: modal, channelID: intr.channelID, sourceMessageID: intr.sourceMessageID}
	s.emitModalLocked(modal)

	s.answerLocked(intr, callbackModal, Result{Modal: &modal})

	return succeed(http.StatusNoContent, nil)
}

// webhookInteractionLocked resolves the interaction a webhook path names.
func (s *Server) webhookInteractionLocked(r *http.Request) (*interaction, *outcome) {
	if r.PathValue("app") != s.appID {
		failure := fail(http.StatusNotFound, codeUnknownApplication, unknownApplicationFn)
		return nil, &failure
	}

	intr, found := s.w.interactions[r.PathValue("token")]
	if !found || !intr.isLive(time.Now()) {
		failure := fail(http.StatusNotFound, codeUnknownInteraction, interactionExpiredText)
		return nil, &failure
	}

	return intr, nil
}

// webhookMessageLocked finds the stored message a webhook route names. ref is "@original" or
// a message ID. The returned index is -1 when there is none.
func (s *Server) webhookMessageLocked(intr *interaction, ref string) (int, *outcome) {
	id := ref
	if ref == originalRef {
		id = intr.originalID
	}
	isOwned := intr.state == stateAnswered && intr.ownsMessage(id)
	index := s.w.messageIndex(id)
	if !isOwned || index < 0 {
		failure := fail(http.StatusNotFound, codeUnknownMessage, unknownMessageText)
		return -1, &failure
	}

	return index, nil
}

func (s *Server) handleGetOriginal(w http.ResponseWriter, r *http.Request) {
	s.getWebhookMessage(w, r, originalRef)
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	s.getWebhookMessage(w, r, r.PathValue("message"))
}

func (s *Server) getWebhookMessage(w http.ResponseWriter, r *http.Request, ref string) {
	s.mu.Lock()
	out := s.getWebhookMessageLocked(r, ref)
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) getWebhookMessageLocked(r *http.Request, ref string) outcome {
	intr, problem := s.webhookInteractionLocked(r)
	if problem != nil {
		return *problem
	}
	index, problem := s.webhookMessageLocked(intr, ref)
	if problem != nil {
		return *problem
	}

	return succeed(http.StatusOK, messageToJSON(cloneMessage(s.w.messages[index]), s.botUser()))
}

func (s *Server) handleDeleteOriginal(w http.ResponseWriter, r *http.Request) {
	s.deleteWebhookMessage(w, r, originalRef)
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	s.deleteWebhookMessage(w, r, r.PathValue("message"))
}

func (s *Server) deleteWebhookMessage(w http.ResponseWriter, r *http.Request, ref string) {
	s.mu.Lock()
	out := s.deleteWebhookMessageLocked(r, ref)
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) deleteWebhookMessageLocked(r *http.Request, ref string) outcome {
	intr, problem := s.webhookInteractionLocked(r)
	if problem != nil {
		return *problem
	}
	index, problem := s.webhookMessageLocked(intr, ref)
	if problem != nil {
		return *problem
	}

	id := s.w.messages[index].ID
	s.deleteMessageLocked(index)
	if id == intr.originalID {
		intr.originalID = ""
		intr.originalDeleted = true
	}
	intr.followupIDs = slices.DeleteFunc(intr.followupIDs, func(followup string) bool {
		return followup == id
	})

	return succeed(http.StatusNoContent, nil)
}

func (s *Server) handleEditOriginal(w http.ResponseWriter, r *http.Request) {
	s.editWebhookMessage(w, r, originalRef)
}

func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	s.editWebhookMessage(w, r, r.PathValue("message"))
}

func (s *Server) editWebhookMessage(w http.ResponseWriter, r *http.Request, ref string) {
	var update messageUpdateBody
	if !decodeBody(w, r, &update) {
		return
	}

	s.mu.Lock()
	out := s.editWebhookMessageLocked(r, ref, update)
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) editWebhookMessageLocked(r *http.Request, ref string, update messageUpdateBody) outcome {
	intr, problem := s.webhookInteractionLocked(r)
	if problem != nil {
		return *problem
	}

	sent, mentionsErr := parseAllowedMentions(update.AllowedMentions)
	var sentContent string
	if len(update.Content) > 0 && !isNull(update.Content) {
		// A non-string content leaves the recorded content empty; applyTo refuses the edit below.
		_ = json.Unmarshal(update.Content, &sentContent)
	}
	record := recordInput{
		route:         routeEditMessage,
		source:        sourceEdit,
		interactionID: intr.id,
		sentContent:   sentContent,
		sent:          sent,
	}
	if ref == originalRef {
		record.route = routeEditOriginal
	}

	isDeferredOriginal := ref == originalRef && intr.state == stateAnswered &&
		intr.awaitsOriginal()
	if isDeferredOriginal {
		return s.createDeferredOriginalLocked(intr, update, mentionsErr, record)
	}

	index, problem := s.webhookMessageLocked(intr, ref)
	if problem != nil {
		return *problem
	}
	updated, problems := update.applyTo(s.w.messages[index])
	if mentionsErr != nil {
		problems = append(problems, errors.New("allowed_mentions: invalid"))
	}
	if len(problems) > 0 {
		return problemsOutcome(problems)
	}

	stored := s.editMessageLocked(index, updated, record)

	return succeed(http.StatusOK, messageToJSON(stored, s.botUser()))
}

// createDeferredOriginalLocked turns the first edit of a deferred response into the
// response message itself.
func (s *Server) createDeferredOriginalLocked(intr *interaction, update messageUpdateBody, mentionsErr error, record recordInput) outcome {
	channel, found := s.w.channels[intr.channelID]
	if !found {
		return fail(http.StatusNotFound, codeUnknownChannel, "Unknown Channel")
	}

	base := s.newMessageLocked(channel.id, createMessageBody{})
	base.InteractionID = intr.id
	if intr.ephemeral {
		base.EphemeralFor = intr.actorID
		base.Flags |= messageFlagEphemeral
	}
	created, problems := update.applyTo(base)
	if mentionsErr != nil {
		problems = append(problems, errors.New("allowed_mentions: invalid"))
	}
	if len(problems) > 0 {
		return problemsOutcome(problems)
	}

	stored := s.storeMessageLocked(created, record)
	intr.originalID = stored.ID

	return succeed(http.StatusOK, messageToJSON(stored, s.botUser()))
}

func (s *Server) handleFollowup(w http.ResponseWriter, r *http.Request) {
	var body createMessageBody
	if !decodeBody(w, r, &body) {
		return
	}
	sent, problem := checkMessageBody(body)
	if problem != nil {
		problem.write(w)
		return
	}

	s.mu.Lock()
	out := s.createFollowupLocked(r, body, sent)
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) createFollowupLocked(r *http.Request, body createMessageBody, sent *AllowedMentions) outcome {
	intr, problem := s.webhookInteractionLocked(r)
	if problem != nil {
		return *problem
	}
	if intr.state != stateAnswered {
		return fail(http.StatusNotFound, codeUnknownMessage, unknownMessageText)
	}
	channel, found := s.w.channels[intr.channelID]
	if !found {
		return fail(http.StatusNotFound, codeUnknownChannel, "Unknown Channel")
	}

	// The first followup after a deferred response replaces the "thinking" placeholder, so
	// it takes the visibility the deferral chose.
	replacesPlaceholder := intr.awaitsOriginal()
	isEphemeral := body.Flags&messageFlagEphemeral != 0
	if replacesPlaceholder {
		isEphemeral = intr.ephemeral
	}

	message := s.newMessageLocked(channel.id, body)
	message.InteractionID = intr.id
	if isEphemeral {
		message.EphemeralFor = intr.actorID
		message.Flags |= messageFlagEphemeral
	} else {
		message.Flags &^= messageFlagEphemeral
	}
	stored := s.storeMessageLocked(message, recordInput{
		route:         routeFollowup,
		source:        sourceFollowup,
		interactionID: intr.id,
		sentContent:   body.Content,
		sent:          sent,
	})

	if replacesPlaceholder {
		intr.originalID = stored.ID
	} else {
		intr.followupIDs = append(intr.followupIDs, stored.ID)
	}

	return succeed(http.StatusOK, messageToJSON(stored, s.botUser()))
}
