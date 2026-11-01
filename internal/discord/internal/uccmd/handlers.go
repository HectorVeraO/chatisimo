package uccmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	routeUC            = "/uc"
	routeChatisimoUC   = "/chatisimo/uc"
	routeUCConfigSet   = "/uc-config/set"
	routeUCConfigShow  = "/uc-config/show"
	routeUCConfigClear = "/uc-config/clear"
)

// Service is the part of the uccalendar module that the Discord command handlers call.
type Service interface {
	Check(ctx context.Context, q uccalendar.Query) (uccalendar.Report, error)
	GetGuildConfig(ctx context.Context, scope uccalendar.GuildScope) (uccalendar.GuildConfig, error)
	UpdateGuildConfig(
		ctx context.Context,
		scope uccalendar.GuildScope,
		update uccalendar.GuildConfigUpdate,
	) (uccalendar.GuildConfig, error)
	ClearGuildConfig(
		ctx context.Context,
		scope uccalendar.GuildScope,
		target uccalendar.ClearTarget,
	) (uccalendar.GuildConfig, error)
}

// Handlers owns the Discord slash command handlers for the Universidad Catolica schedule feature.
type Handlers struct {
	service   Service
	location  *time.Location
	templates *TemplateCache
}

// New validates its arguments, reporting every problem together, and returns the UC command
// handlers.
func New(svc Service, location *time.Location) (*Handlers, error) {
	var problems []error
	if svc == nil {
		problems = append(problems, errors.New("uccmd: service is nil"))
	}
	if location == nil {
		problems = append(problems, errors.New("uccmd: location is nil"))
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	h := &Handlers{
		service:  svc,
		location: location,
	}
	return h, nil
}

// SetTemplates attaches an optional live-reloading TemplateCache for /uc and /chatisimo uc report
// rendering. When nil, the embedded default templates are used.
func (h *Handlers) SetTemplates(cache *TemplateCache) {
	if h == nil {
		return
	}

	h.templates = cache
}

// Register mounts the /uc, /chatisimo/uc, and /uc-config/* slash command handlers on r.
func (h *Handlers) Register(r *router.Router) {
	if r == nil {
		panic("uccmd: router is nil")
	}

	r.SlashCommand(routeUC, h.checkCommand)
	r.SlashCommand(routeChatisimoUC, h.checkCommand)
	r.SlashCommand(routeUCConfigSet, h.setConfigCommand)
	r.SlashCommand(routeUCConfigShow, h.showConfigCommand)
	r.SlashCommand(routeUCConfigClear, h.clearConfigCommand)
}

func (h *Handlers) checkCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, _ := requestAndLocale(e.Ctx)

	var guildID string
	if req.InGuild() {
		guildID = req.GuildID().String()
	}

	query, err := ParseCheckOptions(guildID, data)
	if err != nil {
		return err
	}

	report, err := h.service.Check(e.Ctx, query)
	if err != nil {
		return fmt.Errorf("check uc schedule: %w", err)
	}

	reportLoc := MessageLocale(report.Locale)
	text := RenderReportWith(e.Ctx, h.templates, reportLoc, report, h.location)
	replyErr := req.Public(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) setConfigCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, loc := requestAndLocale(e.Ctx)

	scope, err := requireGuildManager(req)
	if err != nil {
		return err
	}

	update, err := ParseConfigUpdateOptions(data)
	if err != nil {
		return err
	}

	cfg, err := h.service.UpdateGuildConfig(e.Ctx, scope, update)
	if err != nil {
		return fmt.Errorf("update uc guild config: %w", err)
	}

	text := RenderGuildConfigIn(loc, ConfigReplySaved, cfg)
	replyErr := req.Ephemeral(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) showConfigCommand(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, loc := requestAndLocale(e.Ctx)

	scope, err := requireGuildManager(req)
	if err != nil {
		return err
	}

	cfg, err := h.service.GetGuildConfig(e.Ctx, scope)
	if err != nil {
		return fmt.Errorf("get uc guild config: %w", err)
	}

	text := RenderGuildConfigIn(loc, ConfigReplyShow, cfg)
	replyErr := req.Ephemeral(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) clearConfigCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, loc := requestAndLocale(e.Ctx)

	scope, err := requireGuildManager(req)
	if err != nil {
		return err
	}

	target, err := ParseClearTargetOption(data)
	if err != nil {
		return err
	}

	cfg, err := h.service.ClearGuildConfig(e.Ctx, scope, target)
	if err != nil {
		return fmt.Errorf("clear uc guild config: %w", err)
	}

	text := RenderGuildConfigIn(loc, ConfigReplyCleared, cfg)
	replyErr := req.Ephemeral(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func requestAndLocale(ctx context.Context) (*router.Request, Locale) {
	req, found := router.FromContext(ctx)
	if !found {
		panic("uccmd: command ran without a request")
	}

	loc := ResolveLocale(req.Locale(), req.GuildLocale())
	req.SetLocale(string(loc))
	return req, loc
}

func requireGuildManager(req *router.Request) (uccalendar.GuildScope, error) {
	if !req.InGuild() {
		return uccalendar.GuildScope{}, ErrGuildOnly
	}

	scope := uccalendar.GuildScope{
		GuildID:        req.GuildID().String(),
		CanManageGuild: req.CanManageGuild(),
	}
	if !scope.CanManageGuild {
		return uccalendar.GuildScope{}, uccalendar.ErrForbidden
	}

	return scope, nil
}

// ParseCheckOptions extracts and type-checks the window and date slash-command options for /uc and
// /chatisimo uc without panicking on malformed option JSON values.
func ParseCheckOptions(guildID string, data discord.SlashCommandInteractionData) (uccalendar.Query, error) {
	window, hasWindow, windowErr := safeOptString(data, windowOptionName, windowOptionName, uccalendar.InputCodeUnknown)
	date, hasDate, dateErr := safeOptString(data, dateOptionName, dateOptionName, uccalendar.InputCodeInvalidDate)
	if hasWindow && hasDate {
		return uccalendar.Query{}, &uccalendar.InputError{
			Field: "query",
			Code:  uccalendar.InputCodeMutuallyExclusive,
		}
	}
	if windowErr != nil {
		return uccalendar.Query{}, windowErr
	}
	if dateErr != nil {
		return uccalendar.Query{}, dateErr
	}

	query := uccalendar.Query{
		GuildID: guildID,
		Window:  uccalendar.WindowKind(window),
		Date:    date,
	}
	return query, nil
}

// ParseConfigUpdateOptions extracts and validates the friend, channel, and locale slash-command
// options for /uc-config set against the interaction's resolved guild members and channels.
func ParseConfigUpdateOptions(data discord.SlashCommandInteractionData) (uccalendar.GuildConfigUpdate, error) {
	friendID, err := extractFriendOption(data)
	if err != nil {
		return uccalendar.GuildConfigUpdate{}, err
	}

	channelID, err := extractChannelOption(data)
	if err != nil {
		return uccalendar.GuildConfigUpdate{}, err
	}

	locale, err := extractLocaleOption(data)
	if err != nil {
		return uccalendar.GuildConfigUpdate{}, err
	}

	update := uccalendar.GuildConfigUpdate{
		FriendUserID: friendID,
		ChannelID:    channelID,
		Locale:       locale,
	}
	return update, nil
}

// ParseClearTargetOption extracts and type-checks the optional target choice for /uc-config clear.
func ParseClearTargetOption(data discord.SlashCommandInteractionData) (uccalendar.ClearTarget, error) {
	target, _, err := safeOptString(
		data,
		clearTargetOptionName,
		clearTargetOptionName,
		uccalendar.InputCodeUnknown,
	)
	if err != nil {
		return "", err
	}

	parsed := uccalendar.ClearTarget(target)
	return parsed, nil
}

func safeOptString(
	data discord.SlashCommandInteractionData,
	name string,
	field string,
	code uccalendar.InputCode,
) (string, bool, error) {
	opt, present := data.Option(name)
	if !present {
		return "", false, nil
	}
	if opt.Type != discord.ApplicationCommandOptionTypeString {
		return "", true, &uccalendar.InputError{Field: field, Code: code}
	}

	var raw string
	err := json.Unmarshal(opt.Value, &raw)
	if err != nil {
		return "", true, &uccalendar.InputError{Field: field, Code: code}
	}

	return raw, true, nil
}

func extractFriendOption(data discord.SlashCommandInteractionData) (*string, error) {
	opt, present := data.Option(friendOptionName)
	if !present {
		return nil, nil //nolint:nilnil // nil pointer indicates an omitted optional field
	}
	if opt.Type != discord.ApplicationCommandOptionTypeUser {
		return nil, ErrInvalidFriend
	}

	id, raw, err := parseOptionSnowflake(opt.Value)
	if err != nil {
		return nil, ErrInvalidFriend
	}

	user, hasUser := data.Resolved.Users[id]
	if !hasUser {
		return nil, ErrInvalidFriend
	}
	_, isMember := data.Resolved.Members[id]
	if !isMember {
		return nil, ErrInvalidFriend
	}
	if user.Bot {
		return nil, ErrFriendIsBot
	}

	return &raw, nil
}

func extractChannelOption(data discord.SlashCommandInteractionData) (*string, error) {
	opt, present := data.Option(channelOptionName)
	if !present {
		return nil, nil //nolint:nilnil // nil pointer indicates an omitted optional field
	}
	if opt.Type != discord.ApplicationCommandOptionTypeChannel {
		return nil, ErrInvalidChannel
	}

	id, raw, err := parseOptionSnowflake(opt.Value)
	if err != nil {
		return nil, ErrInvalidChannel
	}

	ch, hasChannel := data.Resolved.Channels[id]
	if !hasChannel {
		return nil, ErrInvalidChannel
	}
	isTextOrNews := ch.Type == discord.ChannelTypeGuildText || ch.Type == discord.ChannelTypeGuildNews
	if !isTextOrNews {
		return nil, ErrInvalidChannel
	}

	return &raw, nil
}

func parseOptionSnowflake(value json.RawMessage) (snowflake.ID, string, error) {
	var raw string
	err := json.Unmarshal(value, &raw)
	if err != nil {
		return 0, "", err //nolint:wrapcheck // caller maps option errors to domain sentinels
	}
	validateErr := uccalendar.ValidateSnowflake(raw)
	if validateErr != nil {
		return 0, "", validateErr //nolint:wrapcheck // caller maps option errors to domain sentinels
	}
	id, parseErr := snowflake.Parse(raw)
	if parseErr != nil || id == 0 {
		return 0, "", errors.New("invalid snowflake")
	}

	normalized := id.String()
	return id, normalized, nil
}

func extractLocaleOption(data discord.SlashCommandInteractionData) (*string, error) {
	raw, present, err := safeOptString(
		data,
		localeOptionName,
		localeOptionName,
		uccalendar.InputCodeInvalidLocale,
	)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil //nolint:nilnil // nil pointer indicates an omitted optional field
	}

	parsed, parseErr := uccalendar.ParseGuildLocale(raw)
	if parseErr != nil {
		return nil, parseErr //nolint:wrapcheck // domain InputError mapped by TryMapError
	}

	return &parsed, nil
}
