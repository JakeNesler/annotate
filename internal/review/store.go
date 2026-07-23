package review

import (
	"crypto/rand"
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
	session := &Session{
		ID:        id,
		Title:     title,
		Markdown:  markdown,
		HTML:      rendered,
		Status:    StatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)
	if len(s.sessions) >= s.max {
		s.removeOldestLocked()
	}
	s.sessions[id] = session
	return cloneSession(session), nil
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
