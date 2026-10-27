package giftexchangecmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	// rulesValueInputID is the custom ID of the text input of the rule modal.
	rulesValueInputID = "value"
	// rulesUnsetRaw is the canonical text that clears a time rule.
	rulesUnsetRaw = "unset"

	// rulesMaxFieldRunes is Discord's limit on an embed field value.
	rulesMaxFieldRunes = 1024
	// rulesMaxTitleRunes is Discord's limit on an embed title.
	rulesMaxTitleRunes = 256
	// rulesMaxModalTitleRunes is Discord's limit on a modal title.
	rulesMaxModalTitleRunes = 45
	// rulesMaxModalInputRunes is the limit on the text a person types in the rule modal.
	rulesMaxModalInputRunes = 64

	rulesListSeparator = ", "
	rulesPairArrow     = " -> "
)

func (h *Handlers) registerRules(r *router.Router) {
	h.verb(r, "rules", h.rulesCommand, h.autocompleteFor("rules"))
	r.Component(customIDPattern(actionRules), h.rulesButton)
	r.Component(customIDPattern(actionRulePick), h.rulesPick)
	r.Component(customIDPattern(actionRuleOption), h.rulesOption)
	r.Modal(customIDPattern(actionRuleModal), h.rulesModal)
	h.registerExclusions(r)
}

// rulesSnapshot is what the rules view shows: the exchange, its rules as the caller sees them
// and, for the organizer only, the exclusion pairs.
type rulesSnapshot struct {
	view       giftexchange.ExchangeView
	rules      []giftexchange.RuleView
	exclusions []giftexchange.Exclusion
}

// rulesCommand handles /ge rules. The reply is ephemeral.
func (h *Handlers) rulesCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: rules ran without a request")
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "rules", raw)
	if err != nil {
		return err
	}

	c := h.catalog(LocaleEN)
	return h.rulesShow(e.Ctx, c, req, id, "")
}

// rulesButton handles the Rules button of an exchange card. It answers with a deferred
// ephemeral reply, so the card stays as it is.
func (h *Handlers) rulesButton(e *handler.ComponentEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: rules button ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != actionRules {
		return errBadCustomID
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}
	req.SetExchange(parsed.Exchange)

	c := h.catalog(LocaleEN)
	return h.rulesShow(e.Ctx, c, req, parsed.Exchange, "")
}

// rulesPick handles the choice of a rule in the select of the rules view. A time or duration
// rule opens the modal, which must be the first response; an enum or bool rule replaces the
// components with the select of its options.
func (h *Handlers) rulesPick(e *handler.ComponentEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: rule pick ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != actionRulePick {
		return errBadCustomID
	}
	if req.Group == nil {
		return errGuildOnly
	}
	value, err := rulesSelectedValue(e.Data)
	if err != nil {
		return err
	}

	name, err := giftexchange.ParseRuleName(value)
	if err != nil {
		return fmt.Errorf("parse rule name: %w", err)
	}
	req.SetExchange(parsed.Exchange)

	c := h.catalog(LocaleEN)
	rule, err := h.rulesEditable(e.Ctx, c, req, parsed.Exchange, name)
	if err != nil {
		return err
	}

	switch rule.Kind {
	case giftexchange.RuleKindTime, giftexchange.RuleKindDuration:
		return h.rulesOpenModal(e.Ctx, c, req, parsed.Exchange, rule)
	case giftexchange.RuleKindEnum, giftexchange.RuleKindBool:
		return h.rulesOfferOptions(e.Ctx, c, req, parsed.Exchange, rule)
	case giftexchange.RuleKindInt, giftexchange.RuleKindExclusions:
		return &giftexchange.InputError{Field: string(rule.Name), Reason: c.Get(textRulesNotEditable)}
	default:
		return &giftexchange.InputError{Field: string(rule.Name), Reason: c.Get(textRulesNotEditable)}
	}
}

// rulesOption handles the choice of an option for an enum or bool rule.
func (h *Handlers) rulesOption(e *handler.ComponentEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: rule option ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != actionRuleOption {
		return errBadCustomID
	}
	if req.Group == nil {
		return errGuildOnly
	}
	value, err := rulesSelectedValue(e.Data)
	if err != nil {
		return err
	}
	req.SetExchange(parsed.Exchange)

	c := h.catalog(LocaleEN)
	_, err = h.rulesEditable(e.Ctx, c, req, parsed.Exchange, parsed.Rule)
	if err != nil {
		return err
	}

	deferErr := req.DeferUpdate(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	change := giftexchange.RuleChange{Name: parsed.Rule, Raw: strings.TrimSpace(value)}
	return h.rulesApply(e.Ctx, c, req, parsed.Exchange, change)
}

// rulesModal handles the submission of the modal of a time or duration rule.
func (h *Handlers) rulesModal(e *handler.ModalEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: rule modal ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID)
	if err != nil {
		return err
	}
	if parsed.Action != actionRuleModal {
		return errBadCustomID
	}
	if req.Group == nil {
		return errGuildOnly
	}
	req.SetExchange(parsed.Exchange)

	c := h.catalog(LocaleEN)
	rule, err := h.rulesEditable(e.Ctx, c, req, parsed.Exchange, parsed.Rule)
	if err != nil {
		return err
	}

	deferErr := req.DeferUpdate(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	change, err := h.rulesChangeFor(c, rule, e.Data.Text(rulesValueInputID))
	if err != nil {
		return h.rulesRejectInput(e.Ctx, c, req, parsed.Exchange, err)
	}

	return h.rulesApply(e.Ctx, c, req, parsed.Exchange, change)
}

// rulesSelectedValue returns the one value of a string select. Anything else, such as a
// button click on the custom ID of a select, is not something the view could have sent.
func rulesSelectedValue(data discord.ComponentInteractionData) (string, error) {
	selected, isSelect := data.(discord.StringSelectMenuInteractionData)
	if !isSelect || len(selected.Values) != 1 {
		return "", errBadCustomID
	}

	return selected.Values[0], nil
}

// rulesEditable reads the exchange and returns the rule only when the caller may change it now.
// The module re-checks every change; asking first lets a refusal come before any modal or
// select is shown, and tells the kind of the rule.
func (h *Handlers) rulesEditable(
	ctx context.Context, c Catalog, req *router.Request, id giftexchange.ExchangeID, name giftexchange.RuleName,
) (giftexchange.RuleView, error) {
	_, parseErr := giftexchange.ParseRuleName(string(name))
	if parseErr != nil {
		return giftexchange.RuleView{}, fmt.Errorf("parse rule name: %w", parseErr)
	}

	view, rules, err := h.rulesRead(ctx, req, id)
	if err != nil {
		return giftexchange.RuleView{}, err
	}
	if !view.CallerIsOrganizer {
		return giftexchange.RuleView{}, giftexchange.ErrForbidden
	}

	for _, rule := range rules {
		if rule.Name != name {
			continue
		}

		return rulesCheckEditable(c, rule, view.State)
	}

	return giftexchange.RuleView{}, fmt.Errorf("rule %q: %w", name, giftexchange.ErrUnknownRule)
}

func rulesCheckEditable(c Catalog, rule giftexchange.RuleView, state giftexchange.State) (giftexchange.RuleView, error) {
	switch {
	case !rule.Configurable:
		return giftexchange.RuleView{}, giftexchange.ErrRuleFixed
	case rule.Kind == giftexchange.RuleKindExclusions:
		return giftexchange.RuleView{}, &giftexchange.InputError{Field: "rule", Reason: c.Get(textRulesExclusionsHint)}
	case !rule.EditableNow:
		return giftexchange.RuleView{}, &giftexchange.RuleLockedError{Rule: rule.Name, State: state}
	default:
		return rule, nil
	}
}

// rulesRead reads the exchange and its rules as the caller sees them.
func (h *Handlers) rulesRead(
	ctx context.Context, req *router.Request, id giftexchange.ExchangeID,
) (giftexchange.ExchangeView, []giftexchange.RuleView, error) {
	view, err := h.svc.Exchange(ctx, req.Actor, *req.Group, id)
	if err != nil {
		return giftexchange.ExchangeView{}, nil, fmt.Errorf("read exchange: %w", err)
	}

	rules, err := h.svc.Rules(ctx, req.Actor, *req.Group, id)
	if err != nil {
		return giftexchange.ExchangeView{}, nil, fmt.Errorf("read rules: %w", err)
	}

	return view, rules, nil
}

// rulesSnapshotOf reads everything the view shows. The exclusion pairs are read only for the
// organizer, the one caller the module gives them to.
func (h *Handlers) rulesSnapshotOf(
	ctx context.Context, req *router.Request, id giftexchange.ExchangeID,
) (rulesSnapshot, error) {
	view, rules, err := h.rulesRead(ctx, req, id)
	if err != nil {
		return rulesSnapshot{}, err
	}

	snapshot := rulesSnapshot{view: view, rules: rules}
	if view.CallerIsOrganizer {
		exclusions, exclusionsErr := h.svc.Exclusions(ctx, req.Actor, *req.Group, id)
		if exclusionsErr != nil {
			return rulesSnapshot{}, fmt.Errorf("read exclusions: %w", exclusionsErr)
		}
		snapshot.exclusions = exclusions
	}

	return snapshot, nil
}

// rulesShow edits the original reply into the rules view, with notice as its content.
func (h *Handlers) rulesShow(
	ctx context.Context, c Catalog, req *router.Request, id giftexchange.ExchangeID, notice string,
) error {
	snapshot, err := h.rulesSnapshotOf(ctx, req, id)
	if err != nil {
		return err
	}

	embeds := []discord.Embed{rulesEmbed(c, snapshot)}
	components := rulesPickComponents(c, snapshot)
	editErr := req.EditOriginal(ctx, discord.MessageUpdate{
		Content:    &notice,
		Embeds:     &embeds,
		Components: &components,
	})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// rulesApply stores the change and edits the original reply into the refreshed view. Invalid
// input keeps the view and lists every problem under the Not saved line.
func (h *Handlers) rulesApply(
	ctx context.Context, c Catalog, req *router.Request, id giftexchange.ExchangeID, change giftexchange.RuleChange,
) error {
	_, err := h.svc.SetRule(ctx, req.Actor, *req.Group, id, change)
	if err != nil {
		return h.rulesRejectInput(ctx, c, req, id, fmt.Errorf("set rule: %w", err))
	}

	return h.rulesShow(ctx, c, req, id, "")
}

// rulesRejectInput shows the view with the problems of invalid input. Any other error goes to
// the router.
func (h *Handlers) rulesRejectInput(
	ctx context.Context, c Catalog, req *router.Request, id giftexchange.ExchangeID, err error,
) error {
	isInvalidInput := errors.Is(err, giftexchange.ErrInvalidInput) && !errors.Is(err, giftexchange.ErrUnknownRule)
	if !isInvalidInput {
		return err
	}

	notice := MapErrorIn(c.Locale(), req.ID.String(), err).Text
	return h.rulesShow(ctx, c, req, id, notice)
}

// rulesChangeFor turns the text typed in the modal into the change the module takes. Empty
// input clears a time rule; a duration goes through as typed, and the module reads it.
func (h *Handlers) rulesChangeFor(c Catalog, rule giftexchange.RuleView, typed string) (giftexchange.RuleChange, error) {
	field := string(rule.Name)
	isTooLong := utf8.RuneCountInString(typed) > rulesMaxModalInputRunes
	if isTooLong {
		return giftexchange.RuleChange{}, &giftexchange.InputError{Field: field, Reason: c.Get(textRulesTooLongReason)}
	}

	trimmed := strings.TrimSpace(typed)
	if rule.Kind != giftexchange.RuleKindTime {
		return giftexchange.RuleChange{Name: rule.Name, Raw: trimmed}, nil
	}

	when, isEmpty, err := parseWhen(trimmed, h.now(), h.loc)
	if err != nil {
		return giftexchange.RuleChange{}, &giftexchange.InputError{Field: field, Reason: c.Get(textRulesBadTimeReason)}
	}
	if isEmpty {
		return giftexchange.RuleChange{Name: rule.Name, Raw: rulesUnsetRaw}, nil
	}

	change := giftexchange.RuleChange{Name: rule.Name, Raw: when.UTC().Format(time.RFC3339)}
	return change, nil
}

// rulesOpenModal answers the pick of a time or duration rule with its modal. The modal is
// never prefilled.
func (h *Handlers) rulesOpenModal(
	ctx context.Context, c Catalog, req *router.Request, id giftexchange.ExchangeID, rule giftexchange.RuleView,
) error {
	label, _ := rulesLabel(c, rule.Name)
	placeholder := c.Get(textModalPlaceholderTime)
	if rule.Kind == giftexchange.RuleKindDuration {
		placeholder = c.Get(textModalPlaceholderReminder)
	}

	input := discord.TextInputComponent{
		CustomID:    rulesValueInputID,
		Style:       discord.TextInputStyleShort,
		MaxLength:   rulesMaxModalInputRunes,
		Required:    false,
		Placeholder: placeholder,
	}
	modal := discord.ModalCreate{
		CustomID: formatCustomID(customID{Action: actionRuleModal, Exchange: id, Rule: rule.Name}),
		Title:    render.Truncate(c.Format(textModalTitle, label), rulesMaxModalTitleRunes, truncationMark),
	}.AddLabel(c.Get(textModalLabel), input)

	modalErr := req.Modal(ctx, modal)
	if modalErr != nil {
		return modalErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// rulesOfferOptions answers the pick of an enum or bool rule by replacing the components of
// the view with the select of its options.
func (h *Handlers) rulesOfferOptions(
	ctx context.Context, c Catalog, req *router.Request, id giftexchange.ExchangeID, rule giftexchange.RuleView,
) error {
	label, _ := rulesLabel(c, rule.Name)
	options := make([]discord.StringSelectMenuOption, 0, len(rule.Options))
	for _, option := range rule.Options {
		options = append(options, discord.NewStringSelectMenuOption(rulesOptionLabel(c, option), option))
	}

	menu := discord.NewStringSelectMenu(
		formatCustomID(customID{Action: actionRuleOption, Exchange: id, Rule: rule.Name}),
		render.Truncate(label, maxChoiceRunes, truncationMark),
		options...,
	)
	components := []discord.LayoutComponent{discord.NewActionRow(menu)}

	updateErr := req.UpdateMessage(ctx, discord.MessageUpdate{Components: &components})
	if updateErr != nil {
		return updateErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// rulesPickComponents returns the select of the editable rules, only for the organizer and
// only while there is a rule to offer. The exclusions rule is edited with its own commands.
func rulesPickComponents(c Catalog, s rulesSnapshot) []discord.LayoutComponent {
	components := []discord.LayoutComponent{}
	if !s.view.CallerIsOrganizer {
		return components
	}

	var options []discord.StringSelectMenuOption
	for _, rule := range s.rules {
		isOffered := rule.EditableNow && rule.Kind != giftexchange.RuleKindExclusions
		if !isOffered {
			continue
		}

		label, _ := rulesLabel(c, rule.Name)
		options = append(options, discord.NewStringSelectMenuOption(label, string(rule.Name)))
	}
	if len(options) == 0 {
		return components
	}

	menu := discord.NewStringSelectMenu(
		formatCustomID(customID{Action: actionRulePick, Exchange: s.view.ID}),
		c.Get(textSelectPlaceholder),
		options...,
	)
	components = append(components, discord.NewActionRow(menu))
	return components
}

// rulesEmbed builds the rules view: one field per rule in the module's order, then the
// participants and the exclusions.
func rulesEmbed(c Catalog, s rulesSnapshot) discord.Embed {
	title := render.Truncate(c.Format(textRulesTitle, s.view.Name), rulesMaxTitleRunes, truncationMark)
	description := c.Format(textRulesState, stateText(c, s.view.State), s.view.ParticipantCount, roleText(c, s.view.ExchangeSummary))
	embed := render.Embed(title, description)

	for _, rule := range s.rules {
		label, isKnown := rulesLabel(c, rule.Name)
		if !isKnown {
			label = render.Text(label)
		}

		value := rulesValueText(c, rule) + rulesSuffix(c, rule, s.view.CallerIsOrganizer)
		embed = render.WithField(embed, label, render.Truncate(value, rulesMaxFieldRunes, truncationMark))
	}

	participantsName := c.Format(textParticipantsField, s.view.ParticipantCount)
	embed = render.WithField(embed, participantsName, rulesParticipantsText(c, s.view.Participants))
	embed = render.WithField(embed, c.Get(textExclusionsField), rulesExclusionsText(c, s))
	return embed
}

// rulesLabel returns the label of a rule and whether the label table knows it. An unknown rule
// is labeled with its raw name, so a rule the domain adds renders without a change here.
func rulesLabel(c Catalog, name giftexchange.RuleName) (string, bool) {
	switch name {
	case giftexchange.RuleDrawAt:
		return c.Get(textRuleDrawAt), true
	case giftexchange.RuleRevealAt:
		return c.Get(textRuleRevealAt), true
	case giftexchange.RuleReminderLead:
		return c.Get(textRuleReminderLead), true
	case giftexchange.RuleDropoutPolicy:
		return c.Get(textRuleDropoutPolicy), true
	case giftexchange.RuleAvoidLastRound:
		return c.Get(textRuleAvoidLastRound), true
	case giftexchange.RuleExclusions:
		return c.Get(textRuleExclusions), true
	case giftexchange.RuleQuestionsEnabled:
		return c.Get(textRuleQuestionsEnabled), true
	case giftexchange.RuleMinParticipants:
		return c.Get(textRuleMinParticipants), true
	case giftexchange.RuleMaxParticipants:
		return c.Get(textRuleMaxParticipants), true
	case giftexchange.RuleNoSelfAssignment:
		return c.Get(textRuleNoSelfAssignment), true
	case giftexchange.RuleRedrawBelow:
		return c.Get(textRuleRedrawBelow), true
	case giftexchange.RuleQuestionMaxLength:
		return c.Get(textRuleQuestionMaxLength), true
	case giftexchange.RuleQuestionsPerDay:
		return c.Get(textRuleQuestionsPerDay), true
	default:
		return string(name), false
	}
}

// rulesValueText renders the canonical value of a rule for people.
func rulesValueText(c Catalog, rule giftexchange.RuleView) string {
	switch {
	case rule.Kind == giftexchange.RuleKindTime:
		return rulesTimeText(c, rule.Value)
	case rule.Kind == giftexchange.RuleKindDuration:
		return rulesDurationText(c, rule.Value)
	case rule.Name == giftexchange.RuleNoSelfAssignment:
		return rulesNoSelfAssignmentText(c, rule.Value)
	case rule.Kind == giftexchange.RuleKindBool:
		return rulesFlagText(c, rule.Value)
	case rule.Kind == giftexchange.RuleKindEnum:
		return rulesOptionLabel(c, rule.Value)
	case rule.Name == giftexchange.RuleRedrawBelow:
		return rulesCountText(rule.Value, c.Get(textRedrawBelowValue))
	case rule.Name == giftexchange.RuleQuestionMaxLength:
		return rulesCountText(rule.Value, c.Get(textQuestionLengthValue))
	default:
		return render.Text(rule.Value)
	}
}

// rulesOptionLabel returns the localized label of a bool or enum rule option, falling back to
// escaped plain text for unknown values.
func rulesOptionLabel(c Catalog, option string) string {
	switch option {
	case "on":
		return c.Get(textOn)
	case "off":
		return c.Get(textOff)
	case "patch":
		return c.Get(textDropoutPatch)
	case "redraw":
		return c.Get(textDropoutRedraw)
	default:
		return render.Text(option)
	}
}

// rulesTimeText renders a canonical time as Discord timestamps, and an unset time as manual.
// Text that is not an RFC 3339 time is shown as plain text.
func rulesTimeText(c Catalog, value string) string {
	if value == rulesUnsetRaw {
		return c.Get(textManual)
	}

	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return render.Text(value)
	}

	text := c.Format(textTimeValue, render.TimestampFull(parsed), render.TimestampRelative(parsed))
	return text
}

func rulesDurationText(c Catalog, value string) string {
	isZero := value == "0s" || value == "0"
	if isZero {
		return c.Get(textReminderOff)
	}

	return render.Text(value)
}

func rulesFlagText(c Catalog, value string) string {
	switch value {
	case "on":
		return c.Get(textOn)
	case "off":
		return c.Get(textOff)
	default:
		return render.Text(value)
	}
}

// rulesNoSelfAssignmentText shows the fixed rule the way the label table words it: nobody
// draws themselves.
func rulesNoSelfAssignmentText(c Catalog, value string) string {
	if value == "on" {
		return c.Get(textNever)
	}

	return rulesFlagText(c, value)
}

func rulesCountText(value, format string) string {
	count, err := strconv.Atoi(value)
	if err != nil {
		return render.Text(value)
	}

	text := fmt.Sprintf(format, count)
	return text
}

// rulesSuffix marks a value that is the default, a rule nobody can change, and a rule the
// organizer could change but cannot now.
func rulesSuffix(c Catalog, rule giftexchange.RuleView, isOrganizer bool) string {
	var suffix string
	if rule.Value == rule.Default {
		suffix += c.Get(textSuffixDefault)
	}

	switch {
	case !rule.Configurable:
		suffix += c.Get(textSuffixFixed)
	case isOrganizer && !rule.EditableNow:
		suffix += c.Get(textSuffixLocked)
	}

	return suffix
}

// rulesParticipantsText lists the participants as mentions, cut to fit an embed field with an
// "and N more" tail. A participant without a Discord identity cannot be mentioned and is left
// out.
func rulesParticipantsText(c Catalog, participants []giftexchange.Participant) string {
	mentions := make([]string, 0, len(participants))
	for _, participant := range participants {
		mention, err := discordMention(participant.Person)
		if err != nil {
			continue
		}
		mentions = append(mentions, mention)
	}

	text := rulesFitList(c, mentions, rulesListSeparator)
	if text == "" {
		return c.Get(textNoneYet)
	}

	return text
}

// rulesFitList joins items with separator, dropping items from the end until the text, with an
// "and N more" tail when it dropped any, fits an embed field.
func rulesFitList(c Catalog, items []string, separator string) string {
	for kept := len(items); kept >= 0; kept-- {
		text := strings.Join(items[:kept], separator)
		dropped := len(items) - kept
		if dropped > 0 {
			tail := c.Format(textAndMore, dropped)
			if text == "" {
				text = tail
			} else {
				text += " " + tail
			}
		}

		if utf8.RuneCountInString(text) <= rulesMaxFieldRunes {
			return text
		}
	}

	return ""
}

// rulesExclusionsText is the count for everyone and, for the organizer, one line per pair.
func rulesExclusionsText(c Catalog, s rulesSnapshot) string {
	if !s.view.CallerIsOrganizer || len(s.exclusions) == 0 {
		return strconv.Itoa(s.view.ExclusionCount)
	}

	lines := make([]string, 0, len(s.exclusions))
	unlisted := 0
	for _, exclusion := range s.exclusions {
		giver, giverErr := discordMention(exclusion.Giver)
		recipient, recipientErr := discordMention(exclusion.Recipient)
		if giverErr != nil || recipientErr != nil {
			unlisted++
			continue
		}
		lines = append(lines, giver+rulesPairArrow+recipient)
	}
	if len(lines) == 0 {
		return strconv.Itoa(s.view.ExclusionCount)
	}
	unlisted += max(len(lines)-maxListedLines, 0)
	if len(lines) > maxListedLines {
		lines = lines[:maxListedLines]
	}
	if unlisted > 0 {
		lines = append(lines, c.Format(textAndMore, unlisted))
	}

	text := render.Truncate(strings.Join(lines, "\n"), rulesMaxFieldRunes, truncationMark)
	return text
}
