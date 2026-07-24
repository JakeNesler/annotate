package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

const (
	maxRequestBytes = 2 << 20
	maxTitleBytes   = 200
	maxFeedback     = 500
)

type Server struct {
	store   *Store
	webRoot string
	logger  *slog.Logger
	md      goldmark.Markdown

	// Live site review configuration (empty disables the proxy and preserves
	// pure Markdown behavior). allowedTargets holds exact allowed origins.
	allowedTargets map[string]struct{}
	proxyDomain    string
	proxyTransport http.RoundTripper
}

func NewServer(store *Store, webRoot string, logger *slog.Logger) *Server {
	if store == nil {
		store = NewStore(24*time.Hour, 200)
	}
	if webRoot == "" {
		webRoot = "."
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}
	s := &Server{
		store:          store,
		webRoot:        webRoot,
		logger:         logger,
		md:             goldmark.New(goldmark.WithExtensions(extension.GFM)),
		allowedTargets: allowedTargetsFromEnv(os.Getenv("REVIEW_ALLOWED_TARGETS")),
		proxyDomain:    strings.ToLower(strings.TrimSpace(os.Getenv("REVIEW_PROXY_DOMAIN"))),
		proxyTransport: http.DefaultTransport,
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("POST /api/site-sessions", s.createSiteSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	mux.HandleFunc("POST /api/sessions/{id}/decision", s.decide)
	mux.HandleFunc("GET /r/{id}", s.reviewPage)
	mux.HandleFunc("GET /annotate.js", s.staticFile("annotate.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /assets/{name}", s.asset)
	mux.HandleFunc("GET /", s.launcherPage)
	// The per-session proxy host is detected ahead of the normal router so its
	// responses keep the upstream's headers instead of the review room CSP.
	return s.logRequests(s.proxyRouter(s.securityHeaders(mux)))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input CreateRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Markdown = strings.TrimSpace(input.Markdown)
	if input.Title == "" {
		input.Title = "Untitled review"
	}
	if len(input.Title) > maxTitleBytes {
		writeError(w, http.StatusBadRequest, "title is too long")
		return
	}
	if input.Markdown == "" {
		writeError(w, http.StatusBadRequest, "markdown is required")
		return
	}

	var rendered bytes.Buffer
	if err := s.md.Convert([]byte(input.Markdown), &rendered); err != nil {
		writeError(w, http.StatusInternalServerError, "could not render markdown")
		return
	}
	session, err := s.store.Create(input.Title, input.Markdown, rendered.String())
	if err != nil {
		s.logger.Error("create session", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create review session")
		return
	}
	writeJSON(w, http.StatusCreated, CreateResponse{
		ID:        session.ID,
		URL:       externalBaseURL(r) + "/r/" + session.ID,
		Status:    session.Status,
		ExpiresAt: session.ExpiresAt,
	})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, `/\`) {
		writeError(w, http.StatusNotFound, "review session not found")
		return
	}
	session, err := s.store.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	var input DecisionRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.applyDecision(w, r.PathValue("id"), input.Decision, input.Summary, input.Feedback)
}

// applyDecision validates and records a decision for either a Markdown review
// (main host) or a live site review (reserved proxy endpoint).
func (s *Server) applyDecision(w http.ResponseWriter, id, decision, summary string, feedback []json.RawMessage) {
	summary = strings.TrimSpace(summary)
	if decision != StatusApproved && decision != StatusChangesRequested {
		writeError(w, http.StatusBadRequest, "decision must be approved or changes_requested")
		return
	}
	if len(feedback) > maxFeedback {
		writeError(w, http.StatusBadRequest, "too many feedback items")
		return
	}
	if decision == StatusChangesRequested && summary == "" && len(feedback) == 0 {
		writeError(w, http.StatusBadRequest, "requested changes need a summary or annotation")
		return
	}
	for _, item := range feedback {
		if !json.Valid(item) {
			writeError(w, http.StatusBadRequest, "feedback contains invalid JSON")
			return
		}
	}
	session, err := s.store.Decide(id, Decision{
		Decision: decision,
		Summary:  summary,
		Feedback: feedback,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) launcherPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveHTML(w, r, "static/index.html")
}

func (s *Server) reviewPage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.Get(r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.serveHTML(w, r, "static/review.html")
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	switch name {
	case "styles.css":
		s.staticFile("static/styles.css", "text/css; charset=utf-8")(w, r)
	case "launcher.js", "review.js":
		s.staticFile(filepath.Join("static", name), "text/javascript; charset=utf-8")(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveHTML(w http.ResponseWriter, r *http.Request, name string) {
	s.staticFile(name, "text/html; charset=utf-8")(w, r)
}

func (s *Server) staticFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join(s.webRoot, name))
		if err != nil {
			s.logger.Error("read static file", "name", name, "error", err)
			writeError(w, http.StatusInternalServerError, "review UI is unavailable")
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).Round(time.Millisecond))
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON: multiple values")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDecided):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "review session failed")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSONStatus(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	writeJSONStatus(w, status, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func externalBaseURL(r *http.Request) string {
	return forwardedScheme(r) + "://" + r.Host
}

// forwardedScheme resolves the request's external scheme: HTTP unless the
// forwarded request is HTTPS.
func forwardedScheme(r *http.Request) string {
	if proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); proto == "https" {
		return "https"
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
