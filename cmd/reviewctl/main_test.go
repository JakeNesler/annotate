package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JakeNesler/annotate/internal/review"
)

func TestWriteDecisionText(t *testing.T) {
	var output bytes.Buffer
	now := time.Now()
	session := &review.Session{
		ID:     "abc",
		Status: review.StatusChangesRequested,
		Decision: &review.Decision{
			Decision: review.StatusChangesRequested,
			Summary:  "Tighten the rollout.",
			Feedback: []json.RawMessage{json.RawMessage(
				`{"type":"pin","text":"Add the Flux check","author":"Jake","geom":{"selector":".markdown-body h2","x":0.25,"y":0.5},"replies":[{"author":"Agent","text":"I will add it."}]}`,
			)},
			DecidedAt: now,
		},
	}
	if err := writeDecision(&output, session, "text"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		"FEEDBACK RECEIVED",
		"Note: Tighten the rollout.",
		"[pin] Add the Flux check (at .markdown-body h2, 25% across / 50% down) — Jake",
		"reply from Agent: I will add it.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestOpenBrowserSkipsDesktopLauncherInLitewindow(t *testing.T) {
	t.Setenv("LW_SESSION_ID", "session-1")
	if err := openBrowser("http://annotate.lan/r/example"); err == nil {
		t.Fatal("expected Litewindow to use the surfaced clickable link")
	}
}

func TestReviewOpensURLAndReturnsProviderNeutralFeedback(t *testing.T) {
	server := httptest.NewServer(review.NewServer(
		review.NewStore(time.Hour, 10),
		t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	).Handler())
	t.Cleanup(server.Close)

	document := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(document, []byte("# Plan\n\nVerify the network path."), 0o600); err != nil {
		t.Fatal(err)
	}

	previousOpenURL := openURL
	t.Cleanup(func() { openURL = previousOpenURL })
	var openedURL string
	openURL = func(url string) error {
		openedURL = url
		id := filepath.Base(url)
		go func() {
			body := strings.NewReader(`{"decision":"changes_requested","summary":"Test from another LAN client.","feedback":[{"type":"highlight","text":"Name the network boundary.","author":"Reviewer","anchor":{"exact":"Verify the network path."}}]}`)
			response, err := http.Post(server.URL+"/api/sessions/"+id+"/decision", "application/json", body)
			if err == nil {
				_ = response.Body.Close()
			}
		}()
		return nil
	}

	var stdout, stderr bytes.Buffer
	exitCode := runReview([]string{
		"--server", server.URL,
		"--interval", "100ms",
		document,
	}, &stdout, &stderr)

	if exitCode != 3 {
		t.Fatalf("exit code = %d, want feedback exit 3\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(openedURL, server.URL+"/r/") {
		t.Fatalf("browser URL = %q, want review room under %s/r/", openedURL, server.URL)
	}
	if strings.Contains(stderr.String(), "Session:") || strings.Contains(stderr.String(), "Waiting for a decision on") {
		t.Fatalf("normal review flow exposed session plumbing:\n%s", stderr.String())
	}
	for _, want := range []string{
		"FEEDBACK RECEIVED",
		"Test from another LAN client.",
		`[highlight] Name the network boundary. (on "Verify the network path.") — Reviewer`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestWriteDecisionClaudeHook(t *testing.T) {
	var output bytes.Buffer
	session := &review.Session{ID: "abc", Status: review.StatusApproved, Decision: &review.Decision{Decision: review.StatusApproved}}
	if err := writeDecision(&output, session, "claude-hook"); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.HookSpecificOutput.HookEventName != "PreToolUse" || payload.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("unexpected hook output: %s", output.String())
	}
}
