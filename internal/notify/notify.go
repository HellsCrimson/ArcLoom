// Package notify delivers ArcLoom operational events to an outbound webhook, so
// an apply failure or scheduled drift is visible outside the UI (where the only
// record is the in-memory drift status and the capped run history).
//
// One destination per project, rendered into whatever shape that destination
// expects (see the Kind* constants): chat services get a readable message, and
// the generic webhook gets the Event verbatim as JSON for anything else to parse.
// Delivery is best-effort by design: callers fire it next to the run record and
// never fail an operation because a chat server was down.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Payload kinds — one per supported destination.
const (
	KindWebhook = "webhook" // the Event as JSON, for anything self-written
	KindSlack   = "slack"   // {"text": …} — Slack, Mattermost, Rocket.Chat
	KindDiscord = "discord" // {"content": …} — Discord channel webhook
	KindFluxer  = "fluxer"  // {"content": …} — Fluxer (Discord-compatible webhook API)
	KindTeams   = "teams"   // Adaptive Card — Microsoft Teams Workflows webhook
	KindMatrix  = "matrix"  // m.room.message — Matrix client-server send endpoint
	KindNtfy    = "ntfy"    // plain-text body + Title/Priority/Tags headers — ntfy topic
)

// kinds is the set of destinations a config may name.
var kinds = map[string]bool{
	KindWebhook: true, KindSlack: true, KindDiscord: true,
	KindFluxer: true, KindTeams: true, KindMatrix: true, KindNtfy: true,
}

// ValidKind reports whether kind names a supported destination.
func ValidKind(kind string) bool { return kinds[strings.TrimSpace(kind)] }

// Event levels, used to pick an icon for the Slack rendering and carried
// verbatim in the generic payload.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Event types.
const (
	TypeApplyFailed    = "apply.failed"
	TypeApplySucceeded = "apply.succeeded"
	TypeDriftDetected  = "drift.detected"
	TypeDriftResolved  = "drift.resolved"
	TypeDriftError     = "drift.error"
	TypeTest           = "test"
)

// Event is one notification: an operational outcome worth telling somebody
// about. It is the wire format of the generic webhook payload, so field names
// are part of the contract.
type Event struct {
	Type      string    `json:"type"`
	Level     string    `json:"level"`
	Project   string    `json:"project"`
	ProjectID string    `json:"projectId"`
	Env       string    `json:"env,omitempty"`
	Op        string    `json:"op,omitempty"`
	User      string    `json:"user,omitempty"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
	At        time.Time `json:"at"`
}

// Target is where an event is delivered.
type Target struct {
	Kind string
	URL  string
}

// ValidateURL checks a webhook URL is a plain http(s) endpoint with a host. The
// URL is operator-supplied (like a Git remote), so this is a shape check rather
// than a security boundary — it rejects the obviously wrong (file:, missing
// host) instead of trying to enumerate what an operator may point at.
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("webhook URL is missing a host")
	}
	return nil
}

// NormalizeKind maps a requested payload kind to a supported one, defaulting to
// the generic webhook (which is also what an unrecognized stored value degrades
// to, so an old config can never fail to deliver).
func NormalizeKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kinds[kind] {
		return kind
	}
	return KindWebhook
}

// MaskURL renders a webhook URL for display without leaking its secret path (a
// Slack webhook URL is bearer-equivalent).
func MaskURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Path == "" || u.Path == "/" {
		return u.Scheme + "://" + u.Host
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// client is dedicated so a hanging chat server can't tie up a request for long,
// and so redirects are refused: a webhook that answers with a redirect is a
// misconfiguration, and following one would send the payload somewhere the
// operator never configured.
var client = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("webhook redirected; refusing to follow")
	},
}

// maxErrBody bounds how much of a failing response is read back for the message.
const maxErrBody = 2 << 10

// Send delivers one event. It returns an error on a transport failure or a
// non-2xx response; callers treat that as informational (the operation the
// event describes has already finished).
func Send(ctx context.Context, t Target, ev Event) error {
	if err := ValidateURL(t.URL); err != nil {
		return err
	}
	msg, err := Render(NormalizeKind(t.Kind), ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(t.URL), bytes.NewReader(msg.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", msg.ContentType)
	req.Header.Set("User-Agent", "ArcLoom")
	for k, v := range msg.Headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, maxErrBody))
		detail := strings.TrimSpace(string(msg))
		if detail != "" {
			return fmt.Errorf("webhook returned %s: %s", res.Status, detail)
		}
		return fmt.Errorf("webhook returned %s", res.Status)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxErrBody))
	return nil
}

// Message is a rendered request: what to send and how to label it. Chat
// destinations differ in body shape (and ntfy in content type and headers), so
// each kind produces one of these.
type Message struct {
	Body        []byte
	ContentType string
	Headers     map[string]string
}

// discordLimit is Discord's hard cap on a message's content field; Fluxer mirrors
// the Discord API, so it gets the same treatment.
const discordLimit = 1900

// Render builds the request for a destination. Exported so the payload shapes are
// directly testable without an HTTP round-trip.
func Render(kind string, ev Event) (Message, error) {
	jsonMsg := func(v any) (Message, error) {
		b, err := json.Marshal(v)
		return Message{Body: b, ContentType: "application/json"}, err
	}
	switch NormalizeKind(kind) {
	case KindSlack:
		// Slack's incoming-webhook shape; Mattermost and Rocket.Chat accept it too.
		return jsonMsg(map[string]string{"text": MarkdownText(ev, "*")})
	case KindDiscord:
		return jsonMsg(map[string]string{"content": truncate(MarkdownText(ev, "**"), discordLimit)})
	case KindFluxer:
		// Fluxer's webhook API mirrors Discord's (POST /v1/webhooks/{id}/{token}
		// with a "content" field), so the same payload works.
		return jsonMsg(map[string]string{"content": truncate(MarkdownText(ev, "**"), discordLimit)})
	case KindTeams:
		// Office 365 connectors are retired; a Teams webhook is now a Workflows
		// (Power Automate) trigger, which expects an Adaptive Card attachment.
		return jsonMsg(teamsCard(ev))
	case KindMatrix:
		// Posted straight to the client-server API
		// (POST /_matrix/client/v3/rooms/{roomId}/send/m.room.message), so the body
		// is the event content. A webhook *bridge* (hookshot) should use the slack
		// or generic kind instead.
		return jsonMsg(map[string]string{
			"msgtype":        "m.text",
			"body":           PlainText(ev),
			"format":         "org.matrix.custom.html",
			"formatted_body": matrixHTML(ev),
		})
	case KindNtfy:
		// ntfy publishes a plain-text body to the topic in the URL; the title,
		// priority and emoji live in headers. Header values are ASCII-folded
		// because ntfy reads them as latin-1.
		return Message{
			Body:        []byte(ntfyBody(ev)),
			ContentType: "text/plain; charset=utf-8",
			Headers: map[string]string{
				"Title":    asciiHeader("ArcLoom: " + ev.Summary),
				"Priority": ntfyPriority(ev.Level),
				"Tags":     ntfyTag(ev.Level),
			},
		}, nil
	default:
		return jsonMsg(ev)
	}
}

// MarkdownText renders an event as a single chat message. bold is the emphasis
// marker of the target's flavour of Markdown ("*" for Slack, "**" for Discord).
func MarkdownText(ev Event, bold string) string {
	var b strings.Builder
	b.WriteString(icon(ev.Level))
	b.WriteString(" ")
	b.WriteString(bold)
	b.WriteString("ArcLoom")
	b.WriteString(bold)
	b.WriteString(" — ")
	b.WriteString(ev.Summary)
	if meta := metaLine(ev); meta != "" {
		b.WriteString("\n")
		b.WriteString(meta)
	}
	if ev.Detail != "" {
		b.WriteString("\n```\n")
		b.WriteString(ev.Detail)
		b.WriteString("\n```")
	}
	return b.String()
}

// PlainText renders an event without markup (Matrix's plain body, and the
// fallback any non-Markdown target wants).
func PlainText(ev Event) string {
	var b strings.Builder
	b.WriteString("ArcLoom — ")
	b.WriteString(ev.Summary)
	if meta := metaLine(ev); meta != "" {
		b.WriteString("\n")
		b.WriteString(meta)
	}
	if ev.Detail != "" {
		b.WriteString("\n")
		b.WriteString(ev.Detail)
	}
	return b.String()
}

// metaLine is the "project: x · environment: y · by: z" context line.
func metaLine(ev Event) string {
	meta := []string{}
	if ev.Project != "" {
		meta = append(meta, "project: "+ev.Project)
	}
	if ev.Env != "" {
		meta = append(meta, "environment: "+ev.Env)
	}
	if ev.User != "" {
		meta = append(meta, "by: "+ev.User)
	}
	return strings.Join(meta, " · ")
}

// teamsCard wraps the event in the Adaptive Card message a Teams Workflows
// webhook expects.
func teamsCard(ev Event) map[string]any {
	body := []any{map[string]any{
		"type": "TextBlock", "text": "ArcLoom — " + ev.Summary,
		"weight": "Bolder", "size": "Medium", "wrap": true, "color": teamsColor(ev.Level),
	}}
	if meta := metaLine(ev); meta != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": meta, "isSubtle": true, "wrap": true, "spacing": "Small"})
	}
	if ev.Detail != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": ev.Detail, "wrap": true, "fontType": "Monospace"})
	}
	return map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body":    body,
			},
		}},
	}
}

// teamsColor maps a level onto Adaptive Card's named colours.
func teamsColor(level string) string {
	switch level {
	case LevelError:
		return "Attention"
	case LevelWarn:
		return "Warning"
	default:
		return "Good"
	}
}

// matrixHTML is the formatted_body counterpart of PlainText (values escaped —
// an error message can contain anything).
func matrixHTML(ev Event) string {
	var b strings.Builder
	b.WriteString("<strong>ArcLoom</strong> — ")
	b.WriteString(html.EscapeString(ev.Summary))
	if meta := metaLine(ev); meta != "" {
		b.WriteString("<br/><em>")
		b.WriteString(html.EscapeString(meta))
		b.WriteString("</em>")
	}
	if ev.Detail != "" {
		b.WriteString("<br/><pre><code>")
		b.WriteString(html.EscapeString(ev.Detail))
		b.WriteString("</code></pre>")
	}
	return b.String()
}

// ntfyBody is the message text; the summary already rides in the Title header.
func ntfyBody(ev Event) string {
	parts := []string{}
	if meta := metaLine(ev); meta != "" {
		parts = append(parts, meta)
	}
	if ev.Detail != "" {
		parts = append(parts, ev.Detail)
	}
	if len(parts) == 0 {
		return ev.Summary
	}
	return strings.Join(parts, "\n")
}

// ntfyPriority maps a level to ntfy's priority names: a failure should buzz a
// phone, a success should not.
func ntfyPriority(level string) string {
	switch level {
	case LevelError:
		return "high"
	case LevelWarn:
		return "default"
	default:
		return "low"
	}
}

func ntfyTag(level string) string {
	switch level {
	case LevelError:
		return "rotating_light"
	case LevelWarn:
		return "warning"
	default:
		return "white_check_mark"
	}
}

// asciiHeader folds a value down to printable ASCII so it survives a header that
// is not read as UTF-8 (a project name may contain anything).
func asciiHeader(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		} else if r > 0x7f {
			b.WriteByte('?')
		}
	}
	return b.String()
}

// truncate caps a message at n runes, marking that it was cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…(truncated)"
}

func icon(level string) string {
	switch level {
	case LevelError:
		return ":rotating_light:"
	case LevelWarn:
		return ":warning:"
	default:
		return ":white_check_mark:"
	}
}
