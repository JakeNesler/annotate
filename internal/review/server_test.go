package review

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
