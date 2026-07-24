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

const (
	// KindMarkdown is the legacy document review; KindSite renders a live
	// target SPA through the reverse proxy.
	KindMarkdown = "markdown"
	KindSite     = "site"
)

type Session struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Kind     string `json:"kind,omitempty"`
	Markdown string `json:"-"`
	HTML     string `json:"html"`
	// Target is the absolute origin proxied for a site review (empty for
	// Markdown reviews). DecisionToken gates the reserved proxy decision
	// endpoint and is never serialized to clients.
	Target        string    `json:"-"`
	DecisionToken string    `json:"-"`
	Status        string    `json:"status"`
	Decision      *Decision `json:"decision,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
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

type SiteCreateRequest struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Target string `json:"target"`
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
