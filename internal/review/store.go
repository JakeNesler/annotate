package review

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("review session not found")
	ErrDecided  = errors.New("review session already decided")
)

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
	max      int
	now      func() time.Time
}

func NewStore(ttl time.Duration, maxSessions int) *Store {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if maxSessions <= 0 {
		maxSessions = 200
	}
	return &Store{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		max:      maxSessions,
		now:      time.Now,
	}
}

func (s *Store) Create(title, markdown, rendered string) (*Session, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	return s.add(&Session{
		ID:        id,
		Title:     title,
		Kind:      KindMarkdown,
		Markdown:  markdown,
		HTML:      rendered,
		Status:    StatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}), nil
}

// CreateSite registers a live site review whose target is an absolute origin.
// The session ID is a single DNS-safe label used as the per-session proxy host;
// the decision token gates the reserved proxy decision endpoint.
func (s *Store) CreateSite(title, target string) (*Session, error) {
	id, err := newDNSID()
	if err != nil {
		return nil, err
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	return s.add(&Session{
		ID:            id,
		Title:         title,
		Kind:          KindSite,
		Target:        target,
		DecisionToken: token,
		Status:        StatusPending,
		CreatedAt:     now,
		ExpiresAt:     now.Add(s.ttl),
	}), nil
}

// add inserts a freshly built session under the store lock and returns a clone.
func (s *Store) add(session *Session) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(session.CreatedAt)
	if len(s.sessions) >= s.max {
		s.removeOldestLocked()
	}
	s.sessions[session.ID] = session
	return cloneSession(session)
}

func (s *Store) Get(id string) (*Session, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok || !session.ExpiresAt.After(now) {
		delete(s.sessions, id)
		return nil, ErrNotFound
	}
	return cloneSession(session), nil
}

func (s *Store) Decide(id string, decision Decision) (*Session, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok || !session.ExpiresAt.After(now) {
		delete(s.sessions, id)
		return nil, ErrNotFound
	}
	if session.Status != StatusPending {
		return nil, ErrDecided
	}
	decision.DecidedAt = now
	session.Status = decision.Decision
	session.Decision = &decision
	return cloneSession(session), nil
}

func (s *Store) cleanupLocked(now time.Time) {
	for id, session := range s.sessions {
		if !session.ExpiresAt.After(now) {
			delete(s.sessions, id)
		}
	}
}

func (s *Store) removeOldestLocked() {
	var oldestID string
	var oldest time.Time
	for id, session := range s.sessions {
		if oldestID == "" || session.CreatedAt.Before(oldest) {
			oldestID = id
			oldest = session.CreatedAt
		}
	}
	if oldestID != "" {
		delete(s.sessions, oldestID)
	}
}

func cloneSession(in *Session) *Session {
	out := *in
	if in.Decision != nil {
		decision := *in.Decision
		decision.Feedback = append([]json.RawMessage(nil), in.Decision.Feedback...)
		out.Decision = &decision
	}
	return &out
}

func newID() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// dnsEncoding is lowercase base32 (a-z, 2-7) so the encoded value is a valid,
// case-insensitive DNS label with no padding or separators.
var dnsEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newDNSID returns a cryptographically random identifier usable as one DNS
// label: it starts with a letter and contains only lowercase letters and
// digits, well under the 63-character label limit.
func newDNSID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "s" + dnsEncoding.EncodeToString(buf), nil
}

// newToken returns an unguessable per-session decision token.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// isDNSLabel reports whether host is one syntactically valid DNS label.
func isDNSLabel(host string) bool {
	if host == "" || len(host) > 63 || host[0] == '-' || host[len(host)-1] == '-' {
		return false
	}
	for _, char := range host {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
