package fakediscord

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"unicode/utf8"
)

const (
	controlPrefix = "/_control"

	maxNameLength   = 100
	maxGuildMembers = 1000
	maxUCFixtures   = 1000
	codeUnknown     = 0

	closeCodeNormal = 1000
	closeCodeGoing  = 1001
)

type userControlJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	DMsOpen bool   `json:"dms_open"`
	Locale  string `json:"locale"`
}

type guildControlJSON struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	ChannelID string   `json:"channel_id"`
	Channel   string   `json:"channel"`
	Locale    string   `json:"locale"`
	Members   []string `json:"members"`
}

type stateControlJSON struct {
	Users    []userControlJSON  `json:"users"`
	Guilds   []guildControlJSON `json:"guilds"`
	Bot      string             `json:"bot"`
	Intents  *int               `json:"intents"`
	Commands []string           `json:"commands"`
}

type modalControlJSON struct {
	UserID     string `json:"user_id"`
	CustomID   string `json:"custom_id"`
	Title      string `json:"title"`
	Components any    `json:"components"`
}

type interactionRequest struct {
	As               string            `json:"as"`
	Kind             string            `json:"kind"`
	GuildID          string            `json:"guild_id"`
	Path             string            `json:"path"`
	Options          map[string]any    `json:"options"`
	Focused          string            `json:"focused"`
	MessageID        string            `json:"message_id"`
	CustomID         string            `json:"custom_id"`
	Values           []string          `json:"values"`
	Fields           map[string]string `json:"fields"`
	BypassValidation bool              `json:"bypass_validation"`
	Forged           bool              `json:"forged"`
}

type interactionControlJSON struct {
	InteractionID string            `json:"interaction_id"`
	Status        string            `json:"status"`
	CallbackType  int               `json:"callback_type"`
	MessageID     string            `json:"message_id,omitempty"`
	Choices       []Choice          `json:"choices,omitempty"`
	Modal         *modalControlJSON `json:"modal,omitempty"`
	Error         string            `json:"error,omitempty"`
}

func (s *Server) addControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+controlPrefix+"/healthz", s.handleHealthz)
	mux.HandleFunc("GET "+controlPrefix+"/token", s.handleToken)
	mux.HandleFunc("GET "+controlPrefix+"/state", s.handleState)
	mux.HandleFunc("GET "+controlPrefix+"/commands", s.handleControlCommands)
	mux.HandleFunc("POST "+controlPrefix+"/users", s.handleControlAddUser)
	mux.HandleFunc("PATCH "+controlPrefix+"/users/{id}", s.handleControlPatchUser)
	mux.HandleFunc("POST "+controlPrefix+"/guilds", s.handleControlAddGuild)
	mux.HandleFunc("PATCH "+controlPrefix+"/guilds/{id}", s.handleControlPatchGuild)
	mux.HandleFunc("POST "+controlPrefix+"/guilds/{id}/members", s.handleControlAddMember)
	mux.HandleFunc("POST "+controlPrefix+"/interactions", s.handleControlInteraction)
	mux.HandleFunc("GET "+controlPrefix+"/channels/{id}/messages", s.handleControlChannelMessages)
	mux.HandleFunc("GET "+controlPrefix+"/dms/{user}", s.handleControlDMs)
	mux.HandleFunc("GET "+controlPrefix+"/modals/{user}", s.handleControlModal)
	mux.HandleFunc("GET "+controlPrefix+"/outbound", s.handleControlOutbound)
	mux.HandleFunc("GET "+controlPrefix+"/events", s.handleEvents)
	mux.HandleFunc("POST "+controlPrefix+"/faults", s.handleControlFault)
	mux.HandleFunc("POST "+controlPrefix+"/uc-fixtures", s.handleControlUCFixtures)
	mux.HandleFunc("POST "+controlPrefix+"/gateway/drop", s.handleControlGatewayDrop)
	mux.HandleFunc("POST "+controlPrefix+"/reset", s.handleControlReset)
}

func invalidBody(message string) outcome {
	return fail(http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: "+message)
}

func unknownEntity(what string) outcome {
	return fail(http.StatusNotFound, codeUnknown, "Unknown "+what)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	status := s.botStatusLocked()
	s.mu.Unlock()

	code := http.StatusOK
	if r.URL.Query().Get("bot") == "1" && status != botReady {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]string{"server": "up", "bot": status})
}

func (s *Server) handleToken(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// The client may have gone away; there is nobody to tell.
	_, _ = w.Write([]byte(s.token))
}

func (s *Server) userControlLocked(u User) userControlJSON {
	return userControlJSON{
		ID:      u.ID,
		Name:    u.Name,
		DMsOpen: !s.w.dmsClosed[u.ID],
		Locale:  userLocaleOf(u),
	}
}

func (s *Server) guildControlLocked(g *guildState) guildControlJSON {
	return guildControlJSON{
		ID:        g.ID,
		Name:      g.Name,
		ChannelID: g.ChannelID,
		Channel:   "general",
		Locale:    s.guildLocaleLocked(g.ID),
		Members:   nonNilStrings(slices.Clone(g.members)),
	}
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	state := stateControlJSON{
		Users:    []userControlJSON{},
		Guilds:   []guildControlJSON{},
		Bot:      s.botStatusLocked(),
		Commands: []string{},
	}
	for _, id := range slices.Sorted(maps.Keys(s.w.users)) {
		state.Users = append(state.Users, s.userControlLocked(s.w.users[id]))
	}
	for _, id := range s.w.guildOrder {
		state.Guilds = append(state.Guilds, s.guildControlLocked(s.w.guilds[id]))
	}
	if s.w.intentsSet {
		intents := s.w.intents
		state.Intents = &intents
	}
	for _, command := range s.w.commands {
		if command.GuildID == "" {
			state.Commands = append(state.Commands, command.Name)
		}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleControlCommands(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	definitions := make([]any, 0, len(s.w.commands))
	for _, command := range s.w.commands {
		if command.GuildID == "" {
			definitions = append(definitions, command.Definition)
		}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, definitions)
}

func (s *Server) handleControlAddUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		ID     string `json:"id"`
		Locale string `json:"locale"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	var problems []error
	if utf8.RuneCountInString(body.Name) > maxNameLength {
		problems = append(problems, fmt.Errorf("name: longer than %d characters", maxNameLength))
	}
	if utf8.RuneCountInString(body.Locale) > maxNameLength {
		problems = append(problems, fmt.Errorf("locale: longer than %d characters", maxNameLength))
	}
	if body.ID != "" && !isSnowflake(body.ID) {
		problems = append(problems, errors.New("id: must be a snowflake"))
	}
	if len(problems) > 0 {
		problemsOutcome(problems).write(w)
		return
	}

	stored := s.AddUser(User{ID: body.ID, Name: body.Name, Locale: body.Locale})

	s.mu.Lock()
	out := succeed(http.StatusOK, s.userControlLocked(stored))
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) handleControlPatchUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DMsOpen *bool   `json:"dms_open"`
		Locale  *string `json:"locale"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.DMsOpen == nil && body.Locale == nil {
		invalidBody("dms_open: required").write(w)
		return
	}
	if body.Locale != nil && (*body.Locale == "" || utf8.RuneCountInString(*body.Locale) > maxNameLength) {
		invalidBody(fmt.Sprintf("locale: must be 1-%d characters", maxNameLength)).write(w)
		return
	}

	s.mu.Lock()
	user, found := s.w.users[r.PathValue("id")]
	s.mu.Unlock()
	if !found {
		unknownEntity("user").write(w)
		return
	}

	if body.DMsOpen != nil {
		setErr := s.SetDMsOpen(user, *body.DMsOpen)
		if setErr != nil {
			unknownEntity("user").write(w)
			return
		}
	}
	if body.Locale != nil {
		localeErr := s.SetUserLocale(user, *body.Locale)
		if localeErr != nil {
			unknownEntity("user").write(w)
			return
		}
	}

	s.mu.Lock()
	out := succeed(http.StatusOK, s.userControlLocked(s.w.users[user.ID]))
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) handleControlAddGuild(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		Locale  string   `json:"locale"`
		Members []string `json:"members"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	s.mu.Lock()
	out := s.addGuildLocked(body.Name, body.Locale, body.Members)
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) handleControlPatchGuild(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Locale *string `json:"locale"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Locale == nil || *body.Locale == "" || utf8.RuneCountInString(*body.Locale) > maxNameLength {
		invalidBody(fmt.Sprintf("locale: must be 1-%d characters", maxNameLength)).write(w)
		return
	}

	guildID := r.PathValue("id")
	s.mu.Lock()
	guild, found := s.w.guilds[guildID]
	s.mu.Unlock()
	if !found {
		unknownEntity("guild").write(w)
		return
	}

	setErr := s.SetGuildLocale(guild.Guild, *body.Locale)
	if setErr != nil {
		unknownEntity("guild").write(w)
		return
	}

	s.mu.Lock()
	out := succeed(http.StatusOK, s.guildControlLocked(s.w.guilds[guildID]))
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) addGuildLocked(name, locale string, memberIDs []string) outcome {
	var problems []error
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		problems = append(problems, fmt.Errorf("name: must be 1-%d characters", maxNameLength))
	}
	if utf8.RuneCountInString(locale) > maxNameLength {
		problems = append(problems, fmt.Errorf("locale: longer than %d characters", maxNameLength))
	}
	if len(memberIDs) > maxGuildMembers {
		problems = append(problems, fmt.Errorf("members: at most %d", maxGuildMembers))
	}
	members := make([]User, 0, len(memberIDs))
	for _, id := range memberIDs {
		user, found := s.w.users[id]
		if !found {
			problems = append(problems, fmt.Errorf("members: unknown user %q", id))
			continue
		}
		members = append(members, user)
	}
	if len(problems) > 0 {
		return problemsOutcome(problems)
	}

	stored, _ := s.w.upsertGuild(Guild{Name: name, Locale: locale}, s.ids)
	for _, member := range members {
		err := s.w.addMember(stored.ID, member, s.ids)
		if err != nil {
			panic("fakediscord: guild vanished while adding members: " + err.Error())
		}
	}
	s.announceGuildLocked(stored.ID, "GUILD_CREATE")
	s.publishWorldChangedLocked()

	return succeed(http.StatusOK, s.guildControlLocked(s.w.guilds[stored.ID]))
}

func (s *Server) handleControlAddMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	s.mu.Lock()
	out := s.addControlMemberLocked(r.PathValue("id"), body.UserID)
	s.mu.Unlock()

	out.write(w)
}

func (s *Server) addControlMemberLocked(guildID, userID string) outcome {
	_, hasGuild := s.w.guilds[guildID]
	if !hasGuild {
		return unknownEntity("guild")
	}
	user, hasUser := s.w.users[userID]
	if !hasUser {
		return unknownEntity("user")
	}

	err := s.w.addMember(guildID, user, s.ids)
	if err != nil {
		panic("fakediscord: guild vanished while adding a member: " + err.Error())
	}
	s.publishWorldChangedLocked()

	return succeed(http.StatusNoContent, nil)
}

func (s *Server) handleControlInteraction(w http.ResponseWriter, r *http.Request) {
	var req interactionRequest
	if !decodeBody(w, r, &req) {
		return
	}

	var problems []error
	if req.As == "" {
		problems = append(problems, errors.New("as: required"))
	}
	if !slices.Contains([]string{kindSlash, kindAutocomplete, kindButton, kindSelect, kindModal}, req.Kind) {
		problems = append(problems, fmt.Errorf("kind: must be slash, autocomplete, button, select or modal, got %q", req.Kind))
	}
	isComponent := req.Kind == kindButton || req.Kind == kindSelect
	if isComponent && req.MessageID == "" {
		problems = append(problems, errors.New("message_id: required"))
	}
	if len(problems) > 0 {
		problemsOutcome(problems).write(w)
		return
	}

	result, err := s.runControlAction(r.Context(), req)
	s.interactionOutcome(result, err).write(w)
}

func (s *Server) runControlAction(ctx context.Context, req interactionRequest) (Result, error) {
	as := User{ID: req.As}
	slash := Slash{Path: req.Path, Options: req.Options, BypassValidation: req.BypassValidation}
	if req.GuildID != "" {
		slash.Guild = &Guild{ID: req.GuildID}
	}
	message := Message{ID: req.MessageID}

	switch req.Kind {
	case kindSlash:
		return s.Slash(ctx, as, slash)
	case kindAutocomplete:
		return s.act(ctx, func() (*plan, error) {
			return s.planSlash(as, slash, kindAutocomplete, req.Focused)
		})
	case kindButton:
		return s.act(ctx, func() (*plan, error) {
			return s.planComponent(as, message, req.CustomID, kindButton, nil, req.Forged)
		})
	case kindSelect:
		return s.act(ctx, func() (*plan, error) {
			return s.planComponent(as, message, req.CustomID, kindSelect, req.Values, req.Forged)
		})
	default:
		return s.act(ctx, func() (*plan, error) {
			modal := Modal{CustomID: req.CustomID}
			if modal.CustomID == "" && s.w.modals[req.As] != nil {
				modal.CustomID = s.w.modals[req.As].CustomID
			}
			return s.planModal(as, modal, req.Fields, req.Forged)
		})
	}
}

// interactionOutcome turns the result of an action into the control API's answer.
func (s *Server) interactionOutcome(result Result, err error) outcome {
	switch {
	case errors.Is(err, ErrRejected):
		return succeed(http.StatusOK, interactionControlJSON{Status: StatusRejected, Error: err.Error()})
	case errors.Is(err, ErrNoBot):
		return fail(http.StatusServiceUnavailable, codeUnknown, "No bot session is connected")
	case errors.Is(err, ErrClosed):
		return fail(http.StatusServiceUnavailable, codeUnknown, "Server closed")
	case err != nil:
		return fail(http.StatusRequestTimeout, codeUnknown, "Request ended before the bot answered")
	}

	response := interactionControlJSON{
		InteractionID: result.InteractionID,
		Status:        result.Status,
		CallbackType:  result.CallbackType,
		Choices:       result.Choices,
	}
	if result.Message != nil {
		response.MessageID = result.Message.ID
	}
	if result.Modal != nil {
		modal := modalToJSON(*result.Modal)
		response.Modal = &modal
	}

	return succeed(http.StatusOK, response)
}

func modalToJSON(m Modal) modalControlJSON {
	return modalControlJSON{UserID: m.UserID, CustomID: m.CustomID, Title: m.Title, Components: nonNilRaw(m.Components)}
}

// controlMessageJSON is a message as the control API shows it: the REST message plus what a
// viewer needs to tell who was pinged and which interaction produced it.
type controlMessageJSON struct {
	messageJSON
	InteractionID string   `json:"interaction_id"`
	Pings         []string `json:"pings"`
}

// controlMessageLocked builds the control API view of a message. Its pings are those of the
// newest outbound record about it that was not a deduplicated replay, because an edit can change who is pinged. Callers hold
// s.mu.
func (s *Server) controlMessageLocked(m Message) controlMessageJSON {
	pings := []string{}
	for _, record := range slices.Backward(s.w.records) {
		isCurrent := record.MessageID == m.ID && record.Status == http.StatusOK && !record.Deduplicated
		if isCurrent {
			pings = slices.Clone(record.Pings)
			break
		}
	}

	view := controlMessageJSON{
		messageJSON:   messageToJSON(m, s.botUser()),
		InteractionID: m.InteractionID,
		Pings:         pings,
	}

	return view
}

func (s *Server) controlMessagesLocked(messages []Message) []controlMessageJSON {
	list := make([]controlMessageJSON, 0, len(messages))
	for _, message := range messages {
		list = append(list, s.controlMessageLocked(message))
	}

	return list
}

func (s *Server) handleControlChannelMessages(w http.ResponseWriter, r *http.Request) {
	as := r.URL.Query().Get("as")
	if as == "" {
		invalidBody("as: required").write(w)
		return
	}

	s.mu.Lock()
	_, hasChannel := s.w.channels[r.PathValue("id")]
	_, hasUser := s.w.users[as]
	list := s.controlMessagesLocked(s.w.visibleMessages(as, r.PathValue("id")))
	s.mu.Unlock()

	switch {
	case !hasChannel:
		unknownEntity("channel").write(w)
	case !hasUser:
		unknownEntity("user").write(w)
	default:
		writeJSON(w, http.StatusOK, list)
	}
}

func (s *Server) handleControlDMs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, hasUser := s.w.users[r.PathValue("user")]
	list := s.controlMessagesLocked(s.dmMessagesLocked(r.PathValue("user")))
	s.mu.Unlock()

	if !hasUser {
		unknownEntity("user").write(w)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleControlModal(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pending := s.w.modals[r.PathValue("user")]
	var modal Modal
	if pending != nil {
		modal = pending.Modal
	}
	s.mu.Unlock()

	if pending == nil {
		fail(http.StatusNotFound, codeUnknown, "No modal is open for this user").write(w)
		return
	}
	writeJSON(w, http.StatusOK, modalToJSON(modal))
}

func (s *Server) handleControlOutbound(w http.ResponseWriter, r *http.Request) {
	since := 0
	raw := r.URL.Query().Get("since")
	if raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			invalidBody("since: must be a non-negative integer").write(w)
			return
		}
		since = parsed
	}

	records := []Record{}
	for _, record := range s.Outbound() {
		if record.Seq > since {
			records = append(records, record)
		}
	}
	writeJSON(w, http.StatusOK, records)
}

func (s *Server) handleControlFault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Route  string `json:"route"`
		Status int    `json:"status"`
		Code   int    `json:"code"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	err := s.addFault(body.Route, body.Status, body.Code)
	if err != nil {
		invalidBody(err.Error()).write(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleControlUCFixtures(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fixtures *[]UCFixture `json:"fixtures"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Fixtures == nil {
		invalidBody("fixtures: required").write(w)
		return
	}
	isTooMany := len(*body.Fixtures) > maxUCFixtures
	if isTooMany {
		invalidBody(fmt.Sprintf("fixtures: at most %d", maxUCFixtures)).write(w)
		return
	}

	s.SetUCFixtures(*body.Fixtures)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleControlGatewayDrop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code int `json:"code"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	isCloseCode := body.Code == closeCodeNormal || body.Code == closeCodeGoing ||
		(body.Code >= minCloseCode && body.Code <= maxCloseCode)
	if !isCloseCode {
		invalidBody(fmt.Sprintf("code: %d is not a usable close code", body.Code)).write(w)
		return
	}

	s.DropGateway(body.Code)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleControlReset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.w.reset()
	s.faults = map[string][]fault{}
	s.rephrases = map[string]string{}
	s.ucFixtures = nil
	s.publishWorldChangedLocked()
	s.notifyLocked()
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}
