package capitimecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
)

// Service is the subset of the capitime domain service invoked by Discord slash command handlers
// and message listeners.
type Service interface {
	GetGuildConfig(ctx context.Context, guildID string) (capitime.GuildConfig, error)
	UpdateGuildConfig(
		ctx context.Context,
		scope capitime.GuildScope,
		update capitime.GuildConfigUpdate,
	) (capitime.GuildConfig, error)
	ClearGuildConfig(
		ctx context.Context,
		scope capitime.GuildScope,
		target capitime.ClearTarget,
	) (capitime.GuildConfig, error)
	HandleMessage(ctx context.Context, msg capitime.MessageInput) (capitime.Reply, error)
}

// Handlers wires Capi Time slash command routes onto the Discord interaction router.
type Handlers struct {
	service Service
}

// New validates its arguments and constructs a Handlers set backed by service.
func New(service Service) (*Handlers, error) {
	if service == nil {
		return nil, errors.New("capitimecmd: service is nil")
	}

	return &Handlers{service: service}, nil
}

// Register binds /capitime-config subcommand routes onto r.
func (h *Handlers) Register(r *router.Router) {
	if r == nil {
		panic("capitimecmd: router is nil")
	}

	r.SlashCommand(PathSet, h.handleConfigSet)
	r.SlashCommand(PathShow, h.handleConfigShow)
	r.SlashCommand(PathClear, h.handleConfigClear)
}

func (h *Handlers) handleConfigSet(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, fallbackLoc := requestAndLocale(e.Ctx)

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
		return fmt.Errorf("update capitime guild config: %w", err)
	}

	replyLoc := ResolveLocale(string(cfg.Locale), string(fallbackLoc))
	text := RenderConfigSaved(replyLoc, cfg)
	replyErr := req.Ephemeral(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) handleConfigShow(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, fallbackLoc := requestAndLocale(e.Ctx)

	scope, err := requireGuildManager(req)
	if err != nil {
		return err
	}

	cfg, err := h.service.GetGuildConfig(e.Ctx, scope.GuildID)
	if err != nil {
		return fmt.Errorf("get capitime guild config: %w", err)
	}

	replyLoc := ResolveLocale(string(cfg.Locale), string(fallbackLoc))
	text := RenderConfigShow(replyLoc, cfg)
	replyErr := req.Ephemeral(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) handleConfigClear(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, fallbackLoc := requestAndLocale(e.Ctx)

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
		return fmt.Errorf("clear capitime guild config: %w", err)
	}

	replyLoc := ResolveLocale(string(cfg.Locale), string(fallbackLoc))
	text := RenderConfigCleared(replyLoc, cfg)
	replyErr := req.Ephemeral(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func requestAndLocale(ctx context.Context) (*router.Request, Locale) {
	req, found := router.FromContext(ctx)
	if !found {
		panic("capitimecmd: command ran without a request")
	}

	loc := ResolveLocale(req.Locale(), req.GuildLocale())
	req.SetLocale(string(loc))
	return req, loc
}

func requireGuildManager(req *router.Request) (capitime.GuildScope, error) {
	if !req.InGuild() {
		return capitime.GuildScope{}, ErrGuildOnly
	}

	scope := capitime.GuildScope{
		GuildID:        req.GuildID().String(),
		CanManageGuild: req.CanManageGuild(),
	}
	if !scope.CanManageGuild {
		return capitime.GuildScope{}, capitime.ErrForbidden
	}

	return scope, nil
}

// ParseConfigUpdateOptions extracts and validates the target, channel, and locale options for
// /capitime-config set against the interaction's resolved guild members and channels.
func ParseConfigUpdateOptions(data discord.SlashCommandInteractionData) (capitime.GuildConfigUpdate, error) {
	targetID, err := extractTargetUserOption(data)
	if err != nil {
		return capitime.GuildConfigUpdate{}, err
	}

	channelID, err := extractChannelOption(data)
	if err != nil {
		return capitime.GuildConfigUpdate{}, err
	}

	locale, err := extractLocaleOption(data)
	if err != nil {
		return capitime.GuildConfigUpdate{}, err
	}

	update := capitime.GuildConfigUpdate{
		TargetUserID: targetID,
		ChannelID:    channelID,
		Locale:       locale,
	}
	return update, nil
}

// ParseClearTargetOption extracts and validates the optional target choice for /capitime-config clear.
func ParseClearTargetOption(data discord.SlashCommandInteractionData) (capitime.ClearTarget, error) {
	opt, present := data.Option(OptionClearTarget)
	if !present {
		return capitime.ClearTargetAll, nil
	}
	if opt.Type != discord.ApplicationCommandOptionTypeString {
		return "", &capitime.InputError{Code: capitime.ErrCodeClearTarget}
	}

	var raw string
	if err := json.Unmarshal(opt.Value, &raw); err != nil {
		return "", &capitime.InputError{Code: capitime.ErrCodeClearTarget}
	}
	if raw == "" {
		return capitime.ClearTargetAll, nil
	}

	target := capitime.ClearTarget(raw)
	switch target {
	case capitime.ClearTargetAll, capitime.ClearTargetUser, capitime.ClearTargetChannels, capitime.ClearTargetLocale:
		return target, nil
	default:
		return "", &capitime.InputError{Code: capitime.ErrCodeClearTarget}
	}
}

func extractTargetUserOption(data discord.SlashCommandInteractionData) (*string, error) {
	opt, present := data.Option(OptionUserTarget)
	if !present {
		return nil, nil //nolint:nilnil // nil pointer indicates an omitted optional field
	}
	if opt.Type != discord.ApplicationCommandOptionTypeUser {
		return nil, ErrInvalidTargetUser
	}

	id, raw, err := parseOptionSnowflake(opt.Value)
	if err != nil {
		return nil, ErrInvalidTargetUser
	}

	user, hasUser := data.Resolved.Users[id]
	if !hasUser || user.Bot {
		return nil, ErrInvalidTargetUser
	}
	_, isMember := data.Resolved.Members[id]
	if !isMember {
		return nil, ErrInvalidTargetUser
	}

	return &raw, nil
}

func extractChannelOption(data discord.SlashCommandInteractionData) (*string, error) {
	opt, present := data.Option(OptionChannel)
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

func extractLocaleOption(data discord.SlashCommandInteractionData) (*capitime.Locale, error) {
	opt, present := data.Option(OptionLocale)
	if !present {
		return nil, nil //nolint:nilnil // nil pointer indicates an omitted optional field
	}
	if opt.Type != discord.ApplicationCommandOptionTypeString {
		return nil, &capitime.InputError{Code: capitime.ErrCodeLocale}
	}

	var raw string
	if err := json.Unmarshal(opt.Value, &raw); err != nil {
		return nil, &capitime.InputError{Code: capitime.ErrCodeLocale}
	}

	loc := capitime.Locale(raw)
	if loc != capitime.LocaleESCL && loc != capitime.LocaleENUS {
		return nil, &capitime.InputError{Code: capitime.ErrCodeLocale}
	}

	return &loc, nil
}

func parseOptionSnowflake(value json.RawMessage) (snowflake.ID, string, error) {
	var raw string
	if err := json.Unmarshal(value, &raw); err != nil {
		return 0, "", err //nolint:wrapcheck // caller maps option errors to domain sentinels
	}
	id, parseErr := snowflake.Parse(raw)
	if parseErr != nil || id == 0 {
		return 0, "", errors.New("invalid snowflake")
	}

	normalized := id.String()
	return id, normalized, nil
}
