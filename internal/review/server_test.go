package review

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestReviewLifecycle(t *testing.T) {
	server := httptest.NewServer(NewServer(
		NewStore(time.Hour, 10),
		t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	).Handler())
	t.Cleanup(server.Close)

	created := createReview(t, server.URL, CreateRequest{
		Title:    "Flux rollout",
		Markdown: "# Plan\n\n- Render through Flux\n\n<script>alert('no')</script>",
	})
	if created.ID == "" || !strings.HasPrefix(created.URL, server.URL+"/r/") {
		t.Fatalf("unexpected create response: %+v", created)
	}

	session := getReview(t, server.URL, created.ID)
	if session.Status != StatusPending {
		t.Fatalf("status = %q, want pending", session.Status)
	}
	if !strings.Contains(session.HTML, "<h1>Plan</h1>") {
		t.Fatalf("rendered markdown missing heading: %s", session.HTML)
	}
	if strings.Contains(session.HTML, "<script>") {
		t.Fatalf("unsafe raw HTML was rendered: %s", session.HTML)
	}

	decision := DecisionRequest{
		Decision: StatusChangesRequested,
		Summary:  "Keep the Flux boundary explicit.",
		Feedback: []json.RawMessage{json.RawMessage(`{"type":"pin","text":"Name the verification gate","author":"Jake"}`)},
	}
	response := postJSON(t, server.URL+"/api/sessions/"+created.ID+"/decision", decision)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("decision status = %d, body=%s", response.StatusCode, readBody(response))
	}

	session = getReview(t, server.URL, created.ID)
	if session.Status != StatusChangesRequested || session.Decision == nil {
		t.Fatalf("decision not stored: %+v", session)
	}
	if got := session.Decision.Summary; got != decision.Summary {
		t.Fatalf("summary = %q, want %q", got, decision.Summary)
	}

	response = postJSON(t, server.URL+"/api/sessions/"+created.ID+"/decision", DecisionRequest{Decision: StatusApproved})
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("second decision status = %d, want 409", response.StatusCode)
	}
}

func TestChangesNeedFeedback(t *testing.T) {
	server := httptest.NewServer(NewServer(nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(server.Close)
	created := createReview(t, server.URL, CreateRequest{Markdown: "# Plan"})

	response := postJSON(t, server.URL+"/api/sessions/"+created.ID+"/decision", DecisionRequest{
		Decision: StatusChangesRequested,
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func TestSecurityAndValidation(t *testing.T) {
	server := httptest.NewServer(NewServer(nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Fatalf("missing CSP: %q", got)
	}

	response = postJSON(t, server.URL+"/api/sessions", map[string]string{"markdown": ""})
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty markdown status = %d, want 400", response.StatusCode)
	}
}

func TestSiteSessionRejectsOffAllowlistAndUnknownHostWithoutUpstreamContact(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	t.Setenv("REVIEW_ALLOWED_TARGETS", "http://example.invalid")
	t.Setenv("REVIEW_PROXY_DOMAIN", "review.test")
	server := httptest.NewServer(NewServer(nil, ".", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(server.Close)

	response := postJSON(t, server.URL+"/api/site-sessions", SiteCreateRequest{Target: upstream.URL})
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("off-allowlist status = %d, want 403", response.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "missing.review.test"
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown host status = %d, want 404", response.StatusCode)
	}
	if upstreamHits != 0 {
		t.Fatalf("upstream was contacted %d times", upstreamHits)
	}
}

func TestSiteProxyInjectsHTMLAndForwardsRootRelativeRequests(t *testing.T) {
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://js.stripe.com; connect-src 'self' https://p.anyrentcloud.com")
			_, _ = io.WriteString(w, `<!doctype html><html><body><main id="app">Live SPA</main><script src="/app.js"></script></body></html>`)
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, `window.__appLoaded = true;`)
		case "/api/state":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/images/logo.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	server, host := newSiteProxyTestServer(t, upstream.URL)

	response := getWithHost(t, server.URL+"/", host)
	body := readBody(response)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("html status = %d, body=%s", response.StatusCode, body)
	}
	for _, want := range []string{`/.__annotate/annotate.js`, `/.__annotate/site-review.js`, `Live SPA`} {
		if !strings.Contains(body, want) {
			t.Fatalf("HTML missing %q:\n%s", want, body)
		}
	}
	if csp := response.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' https://js.stripe.com") ||
		!strings.Contains(csp, "connect-src 'self' https://p.anyrentcloud.com") ||
		strings.Contains(csp, "script-src-elem") {
		t.Fatalf("CSP did not preserve the target's script/connect policy: %q", csp)
	}

	for _, requestPath := range []string{"/app.js", "/api/state?route=/rentals", "/images/logo.png"} {
		response = getWithHost(t, server.URL+requestPath, host)
		body = readBody(response)
		response.Body.Close()
		if strings.Contains(body, "/.__annotate/") {
			t.Fatalf("non-HTML response %s was rewritten: %s", requestPath, body)
		}
	}
	for _, want := range []string{"/", "/app.js", "/api/state?route=%2Frentals", "/images/logo.png"} {
		if !contains(paths, want) {
			t.Fatalf("upstream paths %v missing %s", paths, want)
		}
	}
}

func TestSiteProxyRewritesRedirectsAndCookieDomain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://"+r.Host+"/next?from=login")
		w.Header().Add("Set-Cookie", "sid=abc; Domain=127.0.0.1; Path=/; Secure; HttpOnly; SameSite=Lax")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(upstream.Close)
	server, host := newSiteProxyTestServer(t, upstream.URL)

	response := getWithHost(t, server.URL+"/login", host)
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.StatusCode)
	}
	if got := response.Header.Get("Location"); got != "http://"+host+"/next?from=login" {
		t.Fatalf("Location = %q", got)
	}
	cookie := response.Header.Get("Set-Cookie")
	for _, want := range []string{"sid=abc", "Path=/", "Secure", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Fatalf("cookie missing %q: %q", want, cookie)
		}
	}
	if strings.Contains(strings.ToLower(cookie), "domain=") {
		t.Fatalf("cookie domain was not removed: %q", cookie)
	}
}

func TestSiteDecisionTokenAndSessionHostIsolation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><body>ok</body></html>`)
	}))
	t.Cleanup(upstream.Close)
	server, firstHost := newSiteProxyTestServer(t, upstream.URL)
	second := createSiteReview(t, server.URL, SiteCreateRequest{Target: upstream.URL})
	secondHost := hostFromURL(t, second.URL)

	token := siteToken(t, server.URL, firstHost)

	response := postDecisionWithHost(t, server.URL+"/.__annotate/decision", firstHost, "", DecisionRequest{Decision: StatusApproved})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing token status = %d, want 403", response.StatusCode)
	}

	response = postDecisionWithHost(t, server.URL+"/.__annotate/decision", secondHost, token, DecisionRequest{Decision: StatusApproved})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-session token status = %d, want 403", response.StatusCode)
	}

	decision := DecisionRequest{
		Decision: StatusChangesRequested,
		Summary:  "Check the live route.",
		Feedback: []json.RawMessage{json.RawMessage(`{"type":"pin","text":"This renders incorrectly","page":"site:/rentals"}`)},
	}
	response = postDecisionWithHost(t, server.URL+"/.__annotate/decision", firstHost, token, decision)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("valid decision status = %d, want 200", response.StatusCode)
	}

	id := strings.Split(firstHost, ".")[0]
	session := getReview(t, server.URL, id)
	if session.Status != StatusChangesRequested || session.Decision == nil || len(session.Decision.Feedback) != 1 {
		t.Fatalf("decision not stored: %+v", session)
	}
}

func createReview(t *testing.T, base string, request CreateRequest) CreateResponse {
	t.Helper()
	response := postJSON(t, base+"/api/sessions", request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", response.StatusCode, readBody(response))
	}
	var created CreateResponse
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created
}

func createSiteReview(t *testing.T, base string, request SiteCreateRequest) CreateResponse {
	t.Helper()
	response := postJSON(t, base+"/api/site-sessions", request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create site status = %d, body=%s", response.StatusCode, readBody(response))
	}
	var created CreateResponse
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created
}

func newSiteProxyTestServer(t *testing.T, target string) (*httptest.Server, string) {
	t.Helper()
	t.Setenv("REVIEW_ALLOWED_TARGETS", originForTest(t, target))
	t.Setenv("REVIEW_PROXY_DOMAIN", "review.test")
	server := httptest.NewServer(NewServer(
		NewStore(time.Hour, 10),
		".",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	).Handler())
	t.Cleanup(server.Close)
	created := createSiteReview(t, server.URL, SiteCreateRequest{Target: target})
	return server, hostFromURL(t, created.URL)
}

func originForTest(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Scheme + "://" + u.Host
}

func hostFromURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func getWithHost(t *testing.T, rawURL, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func postDecisionWithHost(t *testing.T, rawURL, host, token string, decision DecisionRequest) *http.Response {
	t.Helper()
	body, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Annotate-Decision-Token", token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func siteToken(t *testing.T, base, host string) string {
	t.Helper()
	response := getWithHost(t, base+"/", host)
	defer response.Body.Close()
	body := readBody(response)
	match := regexp.MustCompile(`data-token="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("proxied HTML did not include token: %s", body)
	}
	return match[1]
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func getReview(t *testing.T, base, id string) Session {
	t.Helper()
	response, err := http.Get(base + "/api/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", response.StatusCode, readBody(response))
	}
	var session Session
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	return session
}

func postJSON(t *testing.T, url string, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readBody(response *http.Response) string {
	body, _ := io.ReadAll(response.Body)
	return string(body)
}
