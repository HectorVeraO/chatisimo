package fakediscord

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	maxBodyBytes = 1 << 20

	// Discord error codes the fake answers with.
	codeGeneric            = 0
	codeUnknownApplication = 10002
	codeUnknownChannel     = 10003
	codeUnknownGuild       = 10004
	codeEmptyMessage       = 50006
	codeInvalidFormBody    = 50035
	codeEntityTooLarge     = 40005

	botAuthScheme = "Bot "

	routeCreateMessage = "POST /channels/{id}/messages"

	defaultCommandType = 1
	botUsername        = "Fake Bot"
)

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type userJSON struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	Bot           bool   `json:"bot"`
}

type messageJSON struct {
	ID          string            `json:"id"`
	ChannelID   string            `json:"channel_id"`
	Author      userJSON          `json:"author"`
	Content     string            `json:"content"`
	Timestamp   string            `json:"timestamp"`
	Type        int               `json:"type"`
	Flags       int               `json:"flags"`
	Embeds      []json.RawMessage `json:"embeds"`
	Components  []json.RawMessage `json:"components"`
	Attachments []json.RawMessage `json:"attachments"`
	Mentions    []json.RawMessage `json:"mentions"`
}

type dmChannelJSON struct {
	ID         string     `json:"id"`
	Type       int        `json:"type"`
	Recipients []userJSON `json:"recipients"`
}

type createMessageBody struct {
	Content         string            `json:"content"`
	Embeds          []json.RawMessage `json:"embeds"`
	Components      []json.RawMessage `json:"components"`
	Flags           int               `json:"flags"`
	AllowedMentions json.RawMessage   `json:"allowed_mentions"`
}

func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+restPrefix+"/gateway", s.handleGetGateway)
	mux.HandleFunc("PUT "+restPrefix+"/applications/{app}/commands", s.botOnly(s.handlePutGlobalCommands))
	mux.HandleFunc("PUT "+restPrefix+"/applications/{app}/guilds/{guild}/commands", s.botOnly(s.handlePutGuildCommands))
	mux.HandleFunc("POST "+restPrefix+"/users/@me/channels", s.botOnly(s.handleCreateDM))
	mux.HandleFunc("POST "+restPrefix+"/channels/{channel}/messages", s.botOnly(s.handleCreateMessage))
	mux.HandleFunc("GET "+gatewayPath, s.serveGateway)
	mux.HandleFunc("/", handleNotFound)

	return s.logRequests(mux)
}

// logRequests logs the method, the matched route pattern and the status of every request.
// It never logs the URL, a header or a body: later routes carry interaction tokens in the
// path.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Info("request", "method", r.Method, "route", r.Pattern, "status", recorder.status)
	})
}

// statusRecorder remembers the status code and keeps http.Hijacker available, which the
// WebSocket upgrade needs.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(body)
	if err != nil {
		return n, fmt.Errorf("write response: %w", err)
	}

	return n, nil
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(r.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, fmt.Errorf("hijack: %w", err)
	}
	r.status = http.StatusSwitchingProtocols
	r.wroteHeader = true

	return conn, rw, nil
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		panic("fakediscord: response is not marshalable: " + err.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The client may have gone away; there is nobody to tell.
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, apiError{Code: code, Message: message})
}

func handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, codeGeneric, "404: Not Found")
}

// botOnly requires the Authorization header of the fake's bot.
func (s *Server) botOnly(next http.HandlerFunc) http.HandlerFunc {
	want := botAuthScheme + s.token

	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if !tokensEqual(got, want) {
			writeError(w, http.StatusUnauthorized, codeGeneric, "401: Unauthorized")
			return
		}
		next(w, r)
	}
}

// decodeBody reads a JSON request body of bounded size into target. It answers the client
// itself and returns false when the body is too large or not valid JSON.
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	err := json.NewDecoder(r.Body).Decode(target)
	if err == nil {
		return true
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, codeEntityTooLarge, "Request entity too large")
		return false
	}
	writeError(w, http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body")

	return false
}

func (s *Server) handleGetGateway(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"url": gatewayURL(r)})
}

// gatewayURL is the WebSocket URL for the host the request came to, so it works from the
// host and from inside a container network alike.
func gatewayURL(r *http.Request) string {
	scheme := "ws"
	if r.TLS != nil {
		scheme = "wss"
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1"
	}

	wsURL := scheme + "://" + host + gatewayPath

	return wsURL
}

func (s *Server) handlePutGlobalCommands(w http.ResponseWriter, r *http.Request) {
	s.putCommands(w, r, "")
}

func (s *Server) handlePutGuildCommands(w http.ResponseWriter, r *http.Request) {
	s.putCommands(w, r, r.PathValue("guild"))
}

// putCommands implements the bulk overwrite of the global set (guildID empty) or of one
// guild's set.
func (s *Server) putCommands(w http.ResponseWriter, r *http.Request, guildID string) {
	if r.PathValue("app") != s.appID {
		writeError(w, http.StatusNotFound, codeUnknownApplication, "Unknown Application")
		return
	}

	var posted []map[string]json.RawMessage
	if !decodeBody(w, r, &posted) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if guildID != "" {
		_, found := s.w.guilds[guildID]
		if !found {
			writeError(w, http.StatusNotFound, codeUnknownGuild, "Unknown Guild")
			return
		}
	}

	version := s.ids.next()
	stored, problems := s.buildCommands(posted, guildID, version)
	if len(problems) > 0 {
		message := "Invalid Form Body: " + strings.Join(problems, "; ")
		writeError(w, http.StatusBadRequest, codeInvalidFormBody, message)
		return
	}

	s.w.replaceCommands(guildID, stored)
	if guildID == "" {
		s.w.globalSet = true
	}
	s.notifyLocked()

	definitions := make([]json.RawMessage, 0, len(stored))
	for _, command := range stored {
		definitions = append(definitions, command.Definition)
	}
	writeJSON(w, http.StatusOK, definitions)
}

// buildCommands turns the posted definitions into stored commands, collecting every problem.
func (s *Server) buildCommands(posted []map[string]json.RawMessage, guildID, version string) ([]Command, []string) {
	var problems []string
	commands := make([]Command, 0, len(posted))

	for index, fields := range posted {
		isValid := true

		var name string
		nameErr := json.Unmarshal(fields["name"], &name)
		if nameErr != nil || name == "" {
			problems = append(problems, fmt.Sprintf("commands[%d].name: required string", index))
			isValid = false
		}

		commandType := defaultCommandType
		rawType, hasType := fields["type"]
		isTypeSet := hasType && string(rawType) != "null"
		if isTypeSet {
			typeErr := json.Unmarshal(rawType, &commandType)
			if typeErr != nil {
				problems = append(problems, fmt.Sprintf("commands[%d].type: must be an integer", index))
				isValid = false
			}
		}
		if !isValid {
			continue
		}

		id := s.ids.next()
		fields["id"] = quoted(id)
		fields["application_id"] = quoted(s.appID)
		fields["version"] = quoted(version)
		fields["type"] = json.RawMessage(fmt.Sprint(commandType))
		if guildID != "" {
			fields["guild_id"] = quoted(guildID)
		}

		definition, marshalErr := json.Marshal(fields)
		if marshalErr != nil {
			problems = append(problems, fmt.Sprintf("commands[%d]: not encodable", index))
			continue
		}
		commands = append(commands, Command{
			ID:            id,
			ApplicationID: s.appID,
			GuildID:       guildID,
			Version:       version,
			Name:          name,
			Type:          commandType,
			Definition:    definition,
		})
	}

	return commands, problems
}

func quoted(value string) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("fakediscord: string is not marshalable: " + err.Error())
	}

	return encoded
}

func (w *world) replaceCommands(guildID string, commands []Command) {
	w.commands = slices.DeleteFunc(w.commands, func(c Command) bool {
		return c.GuildID == guildID
	})
	w.commands = append(w.commands, commands...)
}

func (s *Server) handleCreateDM(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RecipientID string `json:"recipient_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	user, found := s.w.users[body.RecipientID]
	if !found {
		writeError(w, http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: recipient_id: unknown user")
		return
	}

	channel := s.w.dmChannelFor(user.ID, s.ids)
	writeJSON(w, http.StatusOK, dmChannelJSON{
		ID:         channel.id,
		Type:       1,
		Recipients: []userJSON{userToJSON(user)},
	})
}

func (s *Server) handleCreateMessage(w http.ResponseWriter, r *http.Request) {
	var body createMessageBody
	if !decodeBody(w, r, &body) {
		return
	}

	sent, mentionsErr := parseAllowedMentions(body.AllowedMentions)
	hasNothing := body.Content == "" && len(body.Embeds) == 0 && len(body.Components) == 0
	var problems []error
	if mentionsErr != nil {
		problems = append(problems, errors.New("allowed_mentions: invalid"))
	}
	if hasNothing {
		problems = append(problems, errors.New("message: cannot send an empty message"))
	}
	if len(problems) > 0 {
		writeCreateMessageProblems(w, mentionsErr != nil, problems)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	channel, found := s.w.channels[r.PathValue("channel")]
	if !found {
		writeError(w, http.StatusNotFound, codeUnknownChannel, "Unknown Channel")
		return
	}

	message := s.storeChannelMessage(channel, body, sent)
	writeJSON(w, http.StatusOK, messageToJSON(message, s.botUser()))
}

// writeCreateMessageProblems answers with every problem found in the request. A malformed
// allowed_mentions is a form-body error; an empty message alone keeps Discord's own code.
func writeCreateMessageProblems(w http.ResponseWriter, hasFormBodyError bool, problems []error) {
	if !hasFormBodyError {
		writeError(w, http.StatusBadRequest, codeEmptyMessage, "Cannot send an empty message")
		return
	}

	joined := strings.ReplaceAll(errors.Join(problems...).Error(), "\n", "; ")
	writeError(w, http.StatusBadRequest, codeInvalidFormBody, "Invalid Form Body: "+joined)
}

// storeChannelMessage appends the message and its outbound record to the world.
func (s *Server) storeChannelMessage(channel *channelState, body createMessageBody, sent *AllowedMentions) Message {
	now := time.Now().UTC()
	message := Message{
		ID:         s.ids.next(),
		ChannelID:  channel.id,
		AuthorID:   s.appID,
		Content:    body.Content,
		Embeds:     nonNilRaw(body.Embeds),
		Components: nonNilRaw(body.Components),
		Flags:      body.Flags,
		CreatedAt:  now,
	}
	s.w.messages = append(s.w.messages, message)

	source := sourceChannel
	var guildID *string
	if channel.guildID != "" {
		id := channel.guildID
		guildID = &id
	} else {
		source = sourceDM
	}

	effective := effectiveMentions(sent)
	texts := append([]string{body.Content}, embedStrings(body.Embeds)...)
	record := Record{
		Seq:                      len(s.w.records) + 1,
		At:                       now,
		Route:                    routeCreateMessage,
		Source:                   source,
		Status:                   http.StatusOK,
		MessageID:                message.ID,
		ChannelID:                channel.id,
		GuildID:                  guildID,
		VisibleTo:                s.w.audience(channel),
		Content:                  body.Content,
		Embeds:                   message.Embeds,
		Components:               message.Components,
		AllowedMentionsSent:      sent,
		AllowedMentionsEffective: effective,
		MentionsRendered:         renderedMentions(texts...),
		Pings:                    pingsFor(body.Content, effective),
	}
	s.w.records = append(s.w.records, record)

	return message
}

func (s *Server) botUser() userJSON {
	return userJSON{ID: s.appID, Username: botUsername, Discriminator: "0", Bot: true}
}

func userToJSON(u User) userJSON {
	return userJSON{ID: u.ID, Username: u.Name, Discriminator: "0"}
}

func messageToJSON(m Message, author userJSON) messageJSON {
	return messageJSON{
		ID:          m.ID,
		ChannelID:   m.ChannelID,
		Author:      author,
		Content:     m.Content,
		Timestamp:   m.CreatedAt.Format(time.RFC3339),
		Type:        0,
		Flags:       m.Flags,
		Embeds:      m.Embeds,
		Components:  m.Components,
		Attachments: []json.RawMessage{},
		Mentions:    []json.RawMessage{},
	}
}

func nonNilRaw(values []json.RawMessage) []json.RawMessage {
	if values == nil {
		return []json.RawMessage{}
	}

	return values
}
