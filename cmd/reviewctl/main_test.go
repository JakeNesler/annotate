package main

import (
	"bytes"
	"encoding/json"
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
			Decision:  review.StatusChangesRequested,
			Summary:   "Tighten the rollout.",
			Feedback:  []json.RawMessage{json.RawMessage(`{"type":"pin","text":"Add the Flux check","author":"Jake"}`)},
			DecidedAt: now,
		},
	}
	if err := writeDecision(&output, session, "text"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{"CHANGES REQUESTED", "Tighten the rollout.", "[pin] Add the Flux check — Jake"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
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
