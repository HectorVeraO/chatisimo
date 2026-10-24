package giftexchange

import "uuid"

// PersonID identifies a person. It is the module's own uuid v7, never a provider ID.
type PersonID uuid.UUID

// GroupID identifies a group (v1: one Discord guild).
type GroupID uuid.UUID

// ExchangeID identifies a gift exchange.
type ExchangeID uuid.UUID

// QuestionID identifies an anonymous question.
type QuestionID uuid.UUID

// NotificationID identifies one notification. A Notifier uses it as its idempotency key.
type NotificationID uuid.UUID

// String returns the canonical 36-character lowercase form.
func (id PersonID) String() string {
	return uuid.UUID(id).String()
}

// IsZero reports the all-zero value, which no stored row ever has.
func (id PersonID) IsZero() bool {
	return id == PersonID{}
}

// String returns the canonical 36-character lowercase form.
func (id GroupID) String() string {
	return uuid.UUID(id).String()
}

// IsZero reports the all-zero value, which no stored row ever has.
func (id GroupID) IsZero() bool {
	return id == GroupID{}
}

// String returns the canonical 36-character lowercase form.
func (id ExchangeID) String() string {
	return uuid.UUID(id).String()
}

// IsZero reports the all-zero value, which no stored row ever has.
func (id ExchangeID) IsZero() bool {
	return id == ExchangeID{}
}

// String returns the canonical 36-character lowercase form.
func (id QuestionID) String() string {
	return uuid.UUID(id).String()
}

// IsZero reports the all-zero value, which no stored row ever has.
func (id QuestionID) IsZero() bool {
	return id == QuestionID{}
}

// String returns the canonical 36-character lowercase form.
func (id NotificationID) String() string {
	return uuid.UUID(id).String()
}

// IsZero reports the all-zero value, which no stored row ever has.
func (id NotificationID) IsZero() bool {
	return id == NotificationID{}
}
