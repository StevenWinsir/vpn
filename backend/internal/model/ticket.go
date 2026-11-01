package model

import "time"

// Ticket is one support request (handoff_ticket.md 4, 5.1). It lives for TicketRetention
// from submission: readers treat it as gone from ExpiresAt on, and the maintenance sweep
// deletes it (its messages go with it through the foreign key). Never serialized directly:
// the user and console endpoints build their own views, so no administrator identity
// reaches a user.
type Ticket struct {
	ID       string `gorm:"type:uuid;primaryKey"`
	TicketNo string `gorm:"size:16;not null;uniqueIndex"`
	UserID   string `gorm:"type:uuid;not null;index:idx_ticket_user_time,priority:1;uniqueIndex:idx_ticket_idempotency,priority:1"`
	// The cascade also covers an older binary's account purge, which does not know tickets.
	User           User    `gorm:"constraint:OnDelete:CASCADE"`
	IdempotencyKey string  `gorm:"type:uuid;not null;uniqueIndex:idx_ticket_idempotency,priority:2"`
	Category       string  `gorm:"size:16;not null;check:category IN ('connection','account','billing','client','other')"`
	Platform       string  `gorm:"size:16;not null;default:'';check:platform IN ('','windows','macos','android','ios','web','other')"`
	OrderID        *string `gorm:"type:uuid"`
	Subject        string  `gorm:"size:240;not null"`
	Status         string  `gorm:"size:12;not null;check:status IN ('open','answered','closed')"`
	UserUnread     bool    `gorm:"not null;default:false"`
	MessageCount   int     `gorm:"not null;default:0;check:message_count >= 0 AND message_count <= 100"`
	// WaitingSince is when the ticket last became open (the console queue serves the
	// longest wait first).
	WaitingSince    *time.Time
	LastMessageAt   time.Time `gorm:"not null;index"`
	ClosedAt        *time.Time
	ClosedBy        string  `gorm:"size:8;not null;default:'';check:closed_by IN ('','user','staff')"`
	ClosedByAdminID *string `gorm:"type:uuid"`
	Version         int64   `gorm:"not null;default:1"`
	// Written from the injected clock, never by GORM's own time.Now.
	CreatedAt time.Time `gorm:"not null;autoCreateTime:false;index:idx_ticket_user_time,priority:2,sort:desc"`
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime:false"`
	ExpiresAt time.Time `gorm:"not null;index"`
}

// TicketMessage is one entry of a ticket's conversation. AuthorID has no foreign key:
// an administrator's account has nothing to do with how long a ticket lives.
type TicketMessage struct {
	ID       string `gorm:"type:uuid;primaryKey"`
	TicketID string `gorm:"type:uuid;not null;uniqueIndex:idx_ticket_message_position,priority:1;uniqueIndex:idx_ticket_message_idempotency,priority:1"`
	Ticket   Ticket `gorm:"constraint:OnDelete:CASCADE"`
	// Position numbers a ticket's messages from 1 in the order they were written (under the
	// ticket's row lock), so the conversation order never depends on two equal timestamps.
	Position   int    `gorm:"not null;uniqueIndex:idx_ticket_message_position,priority:2;check:position >= 1 AND position <= 100"`
	AuthorKind string `gorm:"size:8;not null;check:author_kind IN ('user','staff')"`
	AuthorID   string `gorm:"type:uuid;not null"`
	Body       string `gorm:"type:text;not null"`
	// IdempotencyKey guards a reply against a double submit; the first message of a ticket
	// is covered by the ticket's own key and keeps NULL.
	IdempotencyKey *string   `gorm:"type:uuid;uniqueIndex:idx_ticket_message_idempotency,priority:2"`
	CreatedAt      time.Time `gorm:"not null;autoCreateTime:false"`
}

// Ticket statuses (handoff_ticket.md 4.1): open waits for support, answered waits for the
// user, closed is final.
const (
	TicketOpen     = "open"
	TicketAnswered = "answered"
	TicketClosed   = "closed"

	TicketAuthorUser  = "user"
	TicketAuthorStaff = "staff"

	// TicketRetention is fixed (decision D4): 30 days from submission, never extended.
	TicketRetention = 30 * 24 * time.Hour
	// MaxTicketMessages bounds one conversation (decision D5).
	MaxTicketMessages = 100
)

// TicketCategories and TicketPlatforms are the accepted values, in display order.
var (
	TicketCategories = []string{"connection", "account", "billing", "client", "other"}
	TicketPlatforms  = []string{"windows", "macos", "android", "ios", "web", "other"}
)
