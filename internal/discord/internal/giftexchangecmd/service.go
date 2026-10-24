package giftexchangecmd

import (
	"context"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// Service is the part of the giftexchange module the handlers call. *giftexchange.Service
// satisfies it.
type Service interface {
	ResolveActor(ctx context.Context, ref giftexchange.IdentityRef) (giftexchange.Actor, error)
	ResolvePerson(ctx context.Context, ref giftexchange.IdentityRef) (giftexchange.PersonID, error)
	ResolveGroup(ctx context.Context, ref giftexchange.GroupRef) (giftexchange.GroupID, error)

	CreateExchange(
		ctx context.Context, a giftexchange.Actor, req giftexchange.CreateExchangeRequest,
	) (giftexchange.ExchangeView, error)
	Exchange(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
	) (giftexchange.ExchangeView, error)
	ExchangesFor(
		ctx context.Context, a giftexchange.Actor, g *giftexchange.GroupID,
	) ([]giftexchange.ExchangeSummary, error)
	Rename(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID, name string,
	) error
	Cancel(ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID) error

	Join(ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID) error
	Leave(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
	) (giftexchange.DropoutOutcome, error)
	RemoveParticipant(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
		p giftexchange.PersonID,
	) (giftexchange.DropoutOutcome, error)

	Rules(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
	) ([]giftexchange.RuleView, error)
	SetRule(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
		ch giftexchange.RuleChange,
	) (giftexchange.RuleView, error)
	Exclusions(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
	) ([]giftexchange.Exclusion, error)
	AddExclusion(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
		giver, recipient giftexchange.PersonID, mutual bool,
	) error
	RemoveExclusion(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
		giver, recipient giftexchange.PersonID,
	) error

	Draw(ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID) error
	Reveal(ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID) error
	MyAssignment(ctx context.Context, a giftexchange.Actor, id giftexchange.ExchangeID) (giftexchange.Assignment, error)
	MyGiver(ctx context.Context, a giftexchange.Actor, id giftexchange.ExchangeID) (giftexchange.RevealedGiver, error)

	Ask(ctx context.Context, a giftexchange.Actor, id giftexchange.ExchangeID, text string) (giftexchange.QuestionID, error)
	Answer(ctx context.Context, a giftexchange.Actor, q giftexchange.QuestionID, text string) error
	QuestionsAsked(
		ctx context.Context, a giftexchange.Actor, id giftexchange.ExchangeID,
	) ([]giftexchange.AskedQuestion, error)
	QuestionsReceived(
		ctx context.Context, a giftexchange.Actor, id giftexchange.ExchangeID,
	) ([]giftexchange.ReceivedQuestion, error)
}
