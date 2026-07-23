package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/JakeNesler/annotate/internal/review"
)

const defaultServer = "http://10.0.0.207"

var openURL = openBrowser

type client struct {
	base string
	http *http.Client
}

type decisionOutput struct {
	ID        string            `json:"id"`
	Status    string            `json:"status"`
	Summary   string            `json:"summary,omitempty"`
	Feedback  []json.RawMessage `json:"feedback"`
	DecidedAt *time.Time        `json:"decided_at,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "submit":
		return runSubmit(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "wait":
		return runWait(args[1:], stdout, stderr)
	case "review":
		return runReview(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func runSubmit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", serverDefault(), "review server URL")
	title := fs.String("title", "", "review title")
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: reviewctl submit [flags] <markdown-file|->")
		return 2
	}
	content, err := readInput(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if strings.TrimSpace(*title) == "" {
		*title = titleFromPath(fs.Arg(0))
	}
	c := newClient(*server)
	created, err := c.submit(context.Background(), *title, string(content))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *format == "json" {
		_ = json.NewEncoder(stdout).Encode(created)
		return 0
	}
	if *format != "text" {
		fmt.Fprintln(stderr, "format must be text or json")
		return 2
	}
	fmt.Fprintf(stdout, "Review opened: %s\nSession: %s\nExpires: %s\n", created.URL, created.ID, created.ExpiresAt.Format(time.RFC3339))
	return 0
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", serverDefault(), "review server URL")
	format := fs.String("format", "text", "output format: text, json, or claude-hook")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: reviewctl status [flags] <session-id>")
		return 2
	}
	session, err := newClient(*server).get(context.Background(), fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writeDecision(stdout, session, *format); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return decisionExitCode(session)
}

func runWait(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", serverDefault(), "review server URL")
	format := fs.String("format", "text", "output format: text, json, or claude-hook")
	interval := fs.Duration("interval", 2*time.Second, "poll interval")
	timeout := fs.Duration("timeout", 0, "maximum wait; zero waits indefinitely")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: reviewctl wait [flags] <session-id>")
		return 2
	}
	session, err := newClient(*server).wait(context.Background(), fs.Arg(0), *interval, *timeout)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writeDecision(stdout, session, *format); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return decisionExitCode(session)
}

func runReview(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", serverDefault(), "review server URL")
	title := fs.String("title", "", "review title")
	format := fs.String("format", "text", "decision output format: text, json, or claude-hook")
	interval := fs.Duration("interval", 2*time.Second, "poll interval")
	timeout := fs.Duration("timeout", 0, "maximum wait; zero waits indefinitely")
	noOpen := fs.Bool("no-open", false, "print the review URL without launching a browser")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: reviewctl review [flags] <markdown-file|->")
		return 2
	}
	content, err := readInput(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if strings.TrimSpace(*title) == "" {
		*title = titleFromPath(fs.Arg(0))
	}
	c := newClient(*server)
	created, err := c.submit(context.Background(), *title, string(content))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *noOpen {
		fmt.Fprintf(stderr, "Open this review: %s\n", created.URL)
	} else if err := openURL(created.URL); err != nil {
		fmt.Fprintf(stderr, "Open this review: %s\n", created.URL)
	} else {
		fmt.Fprintf(stderr, "Review opened in your browser: %s\n", created.URL)
	}
	fmt.Fprintln(stderr, "Waiting for your comments…")
	session, err := c.wait(context.Background(), created.ID, *interval, *timeout)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writeDecision(stdout, session, *format); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return decisionExitCode(session)
}

func newClient(server string) *client {
	return &client{
		base: strings.TrimRight(server, "/"),
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *client) submit(ctx context.Context, title, markdown string) (*review.CreateResponse, error) {
	body, err := json.Marshal(review.CreateRequest{Title: title, Markdown: markdown})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/sessions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var created review.CreateResponse
	if err := c.do(req, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *client) get(ctx context.Context, id string) (*review.Session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/sessions/"+id, nil)
	if err != nil {
		return nil, err
	}
	var session review.Session
	if err := c.do(req, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (c *client) wait(ctx context.Context, id string, interval, timeout time.Duration) (*review.Session, error) {
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	for {
		session, err := c.get(ctx, id)
		if err != nil {
			return nil, err
		}
		if session.Status != review.StatusPending {
			return session, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for review decision: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (c *client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("review server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiErr) == nil && apiErr.Error != "" {
			return fmt.Errorf("review server: %s", apiErr.Error)
		}
		return fmt.Errorf("review server returned %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 3<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode review server response: %w", err)
	}
	return nil
}

func writeDecision(w io.Writer, session *review.Session, format string) error {
	out := decisionOutput{
		ID:       session.ID,
		Status:   session.Status,
		Feedback: []json.RawMessage{},
	}
	if session.Decision != nil {
		out.Summary = session.Decision.Summary
		out.Feedback = session.Decision.Feedback
		out.DecidedAt = &session.Decision.DecidedAt
	}

	switch format {
	case "json":
		return json.NewEncoder(w).Encode(out)
	case "claude-hook":
		if session.Status == review.StatusPending {
			return errors.New("claude-hook output requires a final decision; use wait")
		}
		reason := "Approved in Annotate Review"
		permission := "allow"
		if session.Status == review.StatusChangesRequested {
			permission = "deny"
			reason = feedbackReason(out)
		}
		return json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            "PreToolUse",
				"permissionDecision":       permission,
				"permissionDecisionReason": reason,
			},
		})
	case "text":
		if session.Status == review.StatusPending {
			fmt.Fprintln(w, "PENDING")
			return nil
		}
		if session.Status == review.StatusApproved {
			fmt.Fprintln(w, "APPROVED")
		} else {
			fmt.Fprintln(w, "FEEDBACK RECEIVED")
		}
		if out.Summary != "" {
			fmt.Fprintf(w, "Note: %s\n", out.Summary)
		}
		if len(out.Feedback) > 0 {
			fmt.Fprintln(w, "Comments:")
			for _, item := range out.Feedback {
				fmt.Fprintf(w, "- %s\n", describeFeedback(item))
			}
		}
		return nil
	default:
		return errors.New("format must be text, json, or claude-hook")
	}
}

func feedbackReason(out decisionOutput) string {
	var reason strings.Builder
	reason.WriteString("Changes requested in Annotate Review.")
	if out.Summary != "" {
		reason.WriteString(" ")
		reason.WriteString(out.Summary)
	}
	for _, item := range out.Feedback {
		reason.WriteString("\n- ")
		reason.WriteString(describeFeedback(item))
	}
	return reason.String()
}

func describeFeedback(raw json.RawMessage) string {
	var item struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Author string `json:"author"`
		Anchor struct {
			Exact string `json:"exact"`
		} `json:"anchor"`
		Geom struct {
			Selector string   `json:"selector"`
			X        *float64 `json:"x"`
			Y        *float64 `json:"y"`
		} `json:"geom"`
		Replies []struct {
			Author string `json:"author"`
			Text   string `json:"text"`
		} `json:"replies"`
	}
	if json.Unmarshal(raw, &item) == nil && item.Text != "" {
		var description strings.Builder
		if item.Type != "" {
			fmt.Fprintf(&description, "[%s] ", item.Type)
		}
		description.WriteString(item.Text)
		if item.Anchor.Exact != "" {
			fmt.Fprintf(&description, " (on %q)", item.Anchor.Exact)
		} else if item.Geom.Selector != "" {
			fmt.Fprintf(&description, " (at %s", item.Geom.Selector)
			if item.Geom.X != nil && item.Geom.Y != nil {
				fmt.Fprintf(&description, ", %.0f%% across / %.0f%% down", *item.Geom.X*100, *item.Geom.Y*100)
			}
			description.WriteString(")")
		}
		if item.Author != "" {
			fmt.Fprintf(&description, " — %s", item.Author)
		}
		for _, reply := range item.Replies {
			if strings.TrimSpace(reply.Text) == "" {
				continue
			}
			description.WriteString("; reply")
			if reply.Author != "" {
				fmt.Fprintf(&description, " from %s", reply.Author)
			}
			fmt.Fprintf(&description, ": %s", reply.Text)
		}
		return description.String()
	}
	return string(raw)
}

func decisionExitCode(session *review.Session) int {
	if session.Status == review.StatusChangesRequested {
		return 3
	}
	return 0
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(os.Stdin, 2<<20))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > 2<<20 {
		return nil, errors.New("review document exceeds 2 MiB")
	}
	return data, nil
}

func titleFromPath(path string) string {
	if path == "-" {
		return "CLI review"
	}
	name := filepath.Base(path)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func serverDefault() string {
	if value := strings.TrimSpace(os.Getenv("REVIEW_SERVER_URL")); value != "" {
		return value
	}
	return defaultServer
}

func openBrowser(url string) error {
	if strings.TrimSpace(os.Getenv("LW_SESSION_ID")) != "" {
		return errors.New("Litewindow opens links from the active agent message")
	}

	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{url}
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		if strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
			return errors.New("no graphical browser session")
		}
		command = "xdg-open"
		args = []string{url}
	}

	path, err := exec.LookPath(command)
	if err != nil {
		return err
	}
	cmd := exec.Command(path, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `reviewctl — review markdown with a human in the cluster review room

Usage:
  reviewctl review [flags] <file|->   open a browser, then wait for comments
  reviewctl submit [flags] <file|->   create a review and print its URL
  reviewctl status [flags] <id>       print the current decision
  reviewctl wait [flags] <id>         wait for structured reviewer feedback

Set REVIEW_SERVER_URL to override http://10.0.0.207.
The normal review flow is provider-neutral: Claude, Codex, Gemini, Goose, or any
other calling agent receives the same comments on stdout.
Exit code 3 means feedback was returned; it is not an operational failure.`)
}
