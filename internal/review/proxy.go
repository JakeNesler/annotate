package review

import (
	"bytes"
	"compress/gzip"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// annotatePrefix is reserved on the proxy host and never forwarded upstream.
	annotatePrefix = "/.__annotate/"
	annotateBare   = "/.__annotate"
	annotateJSPath = "/.__annotate/annotate.js"
	siteReviewJS   = "/.__annotate/site-review.js"
	decisionPath   = "/.__annotate/decision"

	// maxProxyHTMLBytes caps how much HTML we buffer for injection; larger
	// responses stream through untouched.
	maxProxyHTMLBytes = 8 << 20
)

// allowedTargetsFromEnv parses the comma-separated REVIEW_ALLOWED_TARGETS list
// into a set of exact origins.
func allowedTargetsFromEnv(v string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, part := range strings.Split(v, ",") {
		if origin, _, ok := normalizeTarget(part); ok {
			set[origin] = struct{}{}
		}
	}
	return set
}

// normalizeTarget validates an absolute http(s) URL and splits it into its
// exact origin and the path+query used for the initial proxy URL.
func normalizeTarget(raw string) (origin, pathAndQuery string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" {
		return "", "", false
	}
	origin = scheme + "://" + strings.ToLower(u.Host)
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return origin, p, true
}

// proxyRouter detects the per-session proxy host ahead of the normal router.
func (s *Server) proxyRouter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sid, ok := s.sessionHostID(r.Host); ok {
			s.handleProxy(w, r, sid)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sessionHostID extracts the session label from a "{id}.{proxyDomain}" host.
func (s *Server) sessionHostID(host string) (string, bool) {
	if s.proxyDomain == "" {
		return "", false
	}
	h := hostOnly(host)
	suffix := "." + hostOnly(s.proxyDomain)
	if !strings.HasSuffix(h, suffix) {
		return "", false
	}
	label := h[:len(h)-len(suffix)]
	if !isDNSLabel(label) {
		return "", true
	}
	return label, true
}

func hostOnly(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// handleProxy serves a live site review host: reserved endpoints locally and
// everything else reverse-proxied to the fixed session target.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request, sid string) {
	sess, err := s.store.Get(sid)
	if err != nil || sess.Kind != KindSite {
		// Unknown/expired/non-site hosts are rejected without upstream contact.
		http.Error(w, "review session not found", http.StatusNotFound)
		return
	}
	if r.URL.Path == annotateBare || strings.HasPrefix(r.URL.Path, annotatePrefix) {
		s.handleReserved(w, r, sess)
		return
	}
	s.reverseProxy(w, r, sess)
}

func (s *Server) handleReserved(w http.ResponseWriter, r *http.Request, sess *Session) {
	switch r.URL.Path {
	case annotateJSPath:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.serveProxyAsset(w, "annotate.js", "text/javascript; charset=utf-8")
	case siteReviewJS:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.serveProxyAsset(w, filepath.Join("static", "site-review.js"), "text/javascript; charset=utf-8")
	case decisionPath:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.proxyDecision(w, r, sess)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveProxyAsset(w http.ResponseWriter, name, contentType string) {
	data, err := os.ReadFile(filepath.Join(s.webRoot, name))
	if err != nil {
		s.logger.Error("read proxy asset", "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "review script is unavailable")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// proxyDecision records a decision submitted by the injected site-review script.
// It requires the unguessable per-session token to block casual/cross-session
// forgery (it does not defend against malicious same-realm target JavaScript).
func (s *Server) proxyDecision(w http.ResponseWriter, r *http.Request, sess *Session) {
	var input struct {
		Token    string            `json:"token"`
		Decision string            `json:"decision"`
		Summary  string            `json:"summary"`
		Feedback []json.RawMessage `json:"feedback"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	token := input.Token
	if token == "" {
		token = r.Header.Get("X-Annotate-Decision-Token")
	}
	if sess.DecisionToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(sess.DecisionToken)) != 1 {
		writeError(w, http.StatusForbidden, "invalid review token")
		return
	}
	s.applyDecision(w, sess.ID, input.Decision, input.Summary, input.Feedback)
}

// createSiteSession registers a live site review and returns its per-session
// proxy URL.
func (s *Server) createSiteSession(w http.ResponseWriter, r *http.Request) {
	if s.proxyDomain == "" || len(s.allowedTargets) == 0 {
		writeError(w, http.StatusServiceUnavailable, "live site review is not configured")
		return
	}
	var input SiteCreateRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		input.Title = "Live site review"
	}
	if len(input.Title) > maxTitleBytes {
		writeError(w, http.StatusBadRequest, "title is too long")
		return
	}
	target := strings.TrimSpace(input.Target)
	if target == "" {
		target = strings.TrimSpace(input.URL)
	}
	origin, pathAndQuery, ok := normalizeTarget(target)
	if !ok {
		writeError(w, http.StatusBadRequest, "target must be an absolute http(s) URL")
		return
	}
	if _, allowed := s.allowedTargets[origin]; !allowed {
		writeError(w, http.StatusForbidden, "target origin is not allowed for review")
		return
	}
	session, err := s.store.CreateSite(input.Title, origin)
	if err != nil {
		s.logger.Error("create site session", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create site review session")
		return
	}
	proxyURL := forwardedScheme(r) + "://" + session.ID + "." + s.proxyDomain + pathAndQuery
	writeJSON(w, http.StatusCreated, CreateResponse{
		ID:        session.ID,
		URL:       proxyURL,
		Status:    session.Status,
		ExpiresAt: session.ExpiresAt,
	})
}

// reverseProxy forwards the full request path/query to the fixed session target
// and rewrites the response so root-relative assets, redirects, cookies, and the
// injected review scripts stay on the per-session proxy host.
func (s *Server) reverseProxy(w http.ResponseWriter, r *http.Request, sess *Session) {
	target, err := url.Parse(sess.Target)
	if err != nil {
		s.logger.Error("parse site target", "target", sess.Target, "error", err)
		http.Error(w, "review target is unavailable", http.StatusBadGateway)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// Ask for uncompressed HTML so it can be injected safely.
			pr.Out.Header.Set("Accept-Encoding", "identity")
			pr.SetXForwarded()
		},
		Transport:      s.proxyTransport,
		ModifyResponse: func(resp *http.Response) error { return s.rewriteProxyResponse(r, sess, resp) },
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.logger.Error("proxy upstream", "target", sess.Target, "error", err)
			http.Error(w, "review target is unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) rewriteProxyResponse(inReq *http.Request, sess *Session, resp *http.Response) error {
	scheme := forwardedScheme(inReq)
	proxyHost := inReq.Host

	// Keep same-origin redirects on the proxy; leave off-origin ones alone.
	if loc := resp.Header.Get("Location"); loc != "" {
		if nl, ok := rewriteLocation(loc, sess.Target, scheme, proxyHost); ok {
			resp.Header.Set("Location", nl)
		}
	}

	// Drop an upstream cookie Domain that would not match the proxy host; keep
	// Secure/HttpOnly/SameSite/Path intact.
	if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
		resp.Header.Del("Set-Cookie")
		for _, c := range cookies {
			resp.Header.Add("Set-Cookie", rewriteCookieDomain(c))
		}
	}

	// Preserve the upstream CSP but ensure the same-origin injected scripts and
	// same-origin decision fetch are allowed.
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
		resp.Header.Set("Content-Security-Policy", augmentCSP(csp))
	}

	if isHTMLContentType(resp.Header.Get("Content-Type")) && injectableStatus(resp.StatusCode) {
		return injectHTML(resp, buildInjection(sess))
	}
	return nil
}

// rewriteLocation rewrites a same-origin (target) redirect onto the proxy host.
// Relative or off-origin locations are left unchanged.
func rewriteLocation(loc, targetOrigin, scheme, proxyHost string) (string, bool) {
	u, err := url.Parse(loc)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	if !strings.EqualFold(u.Scheme+"://"+u.Host, targetOrigin) {
		return "", false
	}
	u.Scheme = scheme
	u.Host = proxyHost
	return u.String(), true
}

// rewriteCookieDomain removes the Domain attribute so the cookie becomes
// host-only for the proxy host, preserving every other attribute.
func rewriteCookieDomain(cookie string) string {
	parts := strings.Split(cookie, ";")
	out := parts[:0]
	for _, p := range parts {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), "domain=") {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ";")
}

func isHTMLContentType(ct string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/html")
}

func injectableStatus(code int) bool {
	return code != http.StatusNoContent && code != http.StatusNotModified
}

// buildInjection produces the same-origin script tags injected into upstream
// HTML. The scripts are classic (not deferred) so document.currentScript is set
// when they read their own data attributes.
func buildInjection(sess *Session) []byte {
	var b strings.Builder
	b.WriteString("\n<script src=\"/.__annotate/annotate.js\" data-project=\"")
	b.WriteString(htmlAttr(sess.ID))
	b.WriteString("\" data-position=\"bottom-right\" data-theme=\"auto\" data-start-open=\"true\"></script>\n")
	b.WriteString("<script src=\"/.__annotate/site-review.js\" data-session=\"")
	b.WriteString(htmlAttr(sess.ID))
	b.WriteString("\" data-token=\"")
	b.WriteString(htmlAttr(sess.DecisionToken))
	b.WriteString("\"></script>\n")
	return []byte(b.String())
}

var htmlAttrReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func htmlAttr(v string) string { return htmlAttrReplacer.Replace(v) }

// injectHTML buffers the (optionally gzip-encoded) HTML body up to the cap,
// inserts the review scripts, and rewrites Content-Length. Oversized bodies
// stream through untouched.
func injectHTML(resp *http.Response, snippet []byte) error {
	orig := resp.Body
	head, err := io.ReadAll(io.LimitReader(orig, maxProxyHTMLBytes))
	if err != nil {
		orig.Close()
		return err
	}
	var one [1]byte
	if n, _ := io.ReadFull(orig, one[:]); n > 0 {
		// Too large to inject safely — restore the stream unchanged.
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), bytes.NewReader(one[:n]), orig), orig}
		return nil
	}
	orig.Close()

	raw := head
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" && encoding != "gzip" {
		// The proxy requests identity encoding, but an upstream may ignore it.
		// Preserve unknown encodings instead of injecting into compressed bytes.
		setBody(resp, head)
		return nil
	}
	if encoding == "gzip" {
		decoded := false
		gz, err := gzip.NewReader(bytes.NewReader(head))
		if err == nil {
			if dec, derr := io.ReadAll(gz); derr == nil {
				raw = dec
				decoded = true
				resp.Header.Del("Content-Encoding")
			}
		}
		if !decoded {
			// Could not decode; pass the original bytes through without injection.
			setBody(resp, head)
			return nil
		}
	}
	setBody(resp, injectSnippet(raw, snippet))
	return nil
}

func setBody(resp *http.Response, body []byte) {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

// injectSnippet inserts snippet before </body> (or </html>, or at the end).
func injectSnippet(html, snippet []byte) []byte {
	lower := bytes.ToLower(html)
	idx := bytes.LastIndex(lower, []byte("</body>"))
	if idx < 0 {
		idx = bytes.LastIndex(lower, []byte("</html>"))
	}
	if idx < 0 {
		idx = len(html)
	}
	out := make([]byte, 0, len(html)+len(snippet))
	out = append(out, html[:idx]...)
	out = append(out, snippet...)
	out = append(out, html[idx:]...)
	return out
}

// --- Content-Security-Policy augmentation ------------------------------------

type cspDirective struct {
	name string
	vals []string
}

func parseCSP(csp string) []cspDirective {
	var dirs []cspDirective
	for _, seg := range strings.Split(csp, ";") {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		dirs = append(dirs, cspDirective{name: strings.ToLower(fields[0]), vals: fields[1:]})
	}
	return dirs
}

func serializeCSP(dirs []cspDirective) string {
	parts := make([]string, 0, len(dirs))
	for _, d := range dirs {
		seg := d.name
		if len(d.vals) > 0 {
			seg += " " + strings.Join(d.vals, " ")
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, "; ")
}

// augmentCSP ensures script-src and connect-src both allow 'self' so the
// injected same-origin scripts and the same-origin decision fetch load, without
// discarding the rest of the target's policy.
func augmentCSP(csp string) string {
	dirs := parseCSP(csp)
	dirs = ensureSelfDirective(dirs, "script-src")
	dirs = ensureSelfIfPresent(dirs, "script-src-elem")
	dirs = ensureSelfDirective(dirs, "connect-src")
	return serializeCSP(dirs)
}

func ensureSelfIfPresent(dirs []cspDirective, name string) []cspDirective {
	for i := range dirs {
		if dirs[i].name == name {
			dirs[i].vals = withSelf(dirs[i].vals)
			break
		}
	}
	return dirs
}

func ensureSelfDirective(dirs []cspDirective, name string) []cspDirective {
	for i := range dirs {
		if dirs[i].name == name {
			dirs[i].vals = withSelf(dirs[i].vals)
			return dirs
		}
	}
	for _, d := range dirs {
		if d.name == "default-src" {
			return append(dirs, cspDirective{name: name, vals: withSelf(append([]string(nil), d.vals...))})
		}
	}
	return append(dirs, cspDirective{name: name, vals: []string{"'self'"}})
}

func withSelf(vals []string) []string {
	if len(vals) == 1 && vals[0] == "'none'" {
		return []string{"'self'"}
	}
	filtered := make([]string, 0, len(vals)+1)
	hasSelf := false
	for _, v := range vals {
		if v == "'none'" {
			continue
		}
		if v == "'self'" {
			hasSelf = true
		}
		filtered = append(filtered, v)
	}
	if !hasSelf {
		filtered = append(filtered, "'self'")
	}
	return filtered
}
