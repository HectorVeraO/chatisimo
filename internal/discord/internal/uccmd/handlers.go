package uccmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	routeUC          = "/uc"
	routeChatisimoUC = "/chatisimo/uc"
)

// Service is the part of the uccalendar module that the Discord command handlers call.
type Service interface {
	Check(ctx context.Context, q uccalendar.Query) (uccalendar.Report, error)
}

// Handlers owns the Discord slash command handlers for the Universidad Catolica schedule feature.
type Handlers struct {
	service  Service
	location *time.Location
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

// Register mounts the /uc and /chatisimo/uc slash command handlers on r.
func (h *Handlers) Register(r *router.Router) {
	if r == nil {
		panic("uccmd: router is nil")
	}

	r.SlashCommand(routeUC, h.checkCommand)
	r.SlashCommand(routeChatisimoUC, h.checkCommand)
}

func (h *Handlers) checkCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("uccmd: command ran without a request")
	}

	loc := ResolveLocale(req.Locale(), req.GuildLocale())
	req.SetLocale(string(loc))

	window, _ := data.OptString(windowOptionName)
	date, _ := data.OptString(dateOptionName)

	query := uccalendar.Query{
		Window: uccalendar.WindowKind(window),
		Date:   date,
	}
	report, err := h.service.Check(e.Ctx, query)
	if err != nil {
		return fmt.Errorf("check uc schedule: %w", err)
	}

	text := RenderReportIn(loc, report, h.location)
	replyErr := req.Public(e.Ctx, discord.MessageCreate{Content: text})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}
