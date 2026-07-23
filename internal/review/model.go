package review

import (
	"encoding/json"
	"time"
)

const (
	StatusPending          = "pending"
	StatusApproved         = "approved"
	StatusChangesRequested = "changes_requested"
)

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Markdown  string    `json:"-"`
	HTML      string    `json:"html"`
	Status    string    `json:"status"`
	Decision  *Decision `json:"decision,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Decision struct {
	Decision  string            `json:"decision"`
	Summary   string            `json:"summary,omitempty"`
	Feedback  []json.RawMessage `json:"feedback"`
	DecidedAt time.Time         `json:"decided_at"`
}

type CreateRequest struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

type CreateResponse struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

type DecisionRequest struct {
	Decision string            `json:"decision"`
	Summary  string            `json:"summary"`
	Feedback []json.RawMessage `json:"feedback"`
}
