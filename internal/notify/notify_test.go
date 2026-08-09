package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testEvent() Event {
	return Event{
		Type: TypeApplyFailed, Level: LevelError,
		Project: "homelab", ProjectID: "p1", Env: "prod", Op: "apply", User: "alice",
		Summary: "apply failed", Detail: "Error: quota exceeded",
		At: time.Unix(1700000000, 0).UTC(),
	}
}

func TestValidateURL(t *testing.T) {
	ok := []string{
		"https://hooks.slack.com/services/T000/B000/XXX",
		"http://127.0.0.1:9000/hook",
	}
	for _, u := range ok {
		if err := ValidateURL(u); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{"", "hooks.slack.com/x", "file:///etc/passwd", "ftp://host/x", "https://"}
	for _, u := range bad {
		if err := ValidateURL(u); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want an error", u)
		}
	}
}

func TestMaskURLHidesTheSecretPath(t *testing.T) {
	got := MaskURL("https://hooks.slack.com/services/T000/B000/SECRET")
	if got != "https://hooks.slack.com/…" {
		t.Fatalf("MaskURL = %q", got)
	}
	if strings.Contains(got, "SECRET") {
		t.Fatal("mask leaked the webhook token")
	}
	if got := MaskURL("https://example.com"); got != "https://example.com" {
		t.Fatalf("MaskURL without a path = %q", got)
	}
	if got := MaskURL("not a url at all"); got != "" {
		t.Fatalf("MaskURL of junk = %q, want empty", got)
	}
}

func TestNormalizeKind(t *testing.T) {
	for in, want := range map[string]string{
		"slack": KindSlack, "discord": KindDiscord, "teams": KindTeams,
		"matrix": KindMatrix, "ntfy": KindNtfy, "fluxer": KindFluxer,
		"webhook": KindWebhook,
		// Empty and anything unrecognized degrade to the generic payload rather
		// than failing to deliver.
		"": KindWebhook, "carrier-pigeon": KindWebhook,
	} {
		if got := NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
	if ValidKind("carrier-pigeon") || ValidKind("") {
		t.Error("ValidKind should reject unknown and empty kinds")
	}
	if !ValidKind("ntfy") {
		t.Error("ValidKind should accept a supported kind")
	}
}

// decode is a helper for the per-destination payload assertions.
func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload is not JSON: %v (%s)", err, b)
	}
	return m
}

// Discord (and Fluxer, which mirrors its webhook API) take a "content" string
// with ** emphasis.
func TestRenderDiscordAndFluxer(t *testing.T) {
	for _, kind := range []string{KindDiscord, KindFluxer} {
		msg, err := Render(kind, testEvent())
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if msg.ContentType != "application/json" {
			t.Errorf("%s content type = %q", kind, msg.ContentType)
		}
		body := decode(t, msg.Body)
		content, _ := body["content"].(string)
		if len(body) != 1 || content == "" {
			t.Fatalf("%s payload = %v", kind, body)
		}
		for _, want := range []string{"**ArcLoom**", "apply failed", "project: homelab", "quota exceeded"} {
			if !strings.Contains(content, want) {
				t.Errorf("%s content missing %q:\n%s", kind, want, content)
			}
		}
	}
}

// Discord rejects messages over 2000 characters, so a long detail is truncated.
func TestRenderDiscordTruncates(t *testing.T) {
	ev := testEvent()
	ev.Detail = strings.Repeat("x", 5000)
	msg, err := Render(KindDiscord, ev)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := decode(t, msg.Body)["content"].(string)
	if len([]rune(content)) > 2000 {
		t.Fatalf("content is %d runes, Discord caps at 2000", len([]rune(content)))
	}
	if !strings.Contains(content, "truncated") {
		t.Error("truncation should be marked in the message")
	}
}

// Teams (Workflows) expects an Adaptive Card attachment, not a bare text field.
func TestRenderTeamsAdaptiveCard(t *testing.T) {
	msg, err := Render(KindTeams, testEvent())
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, msg.Body)
	if body["type"] != "message" {
		t.Fatalf("type = %v, want message", body["type"])
	}
	atts, ok := body["attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("attachments = %v", body["attachments"])
	}
	att := atts[0].(map[string]any)
	if att["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Fatalf("contentType = %v", att["contentType"])
	}
	card := att["content"].(map[string]any)
	if card["type"] != "AdaptiveCard" || card["version"] == nil {
		t.Fatalf("card = %v", card)
	}
	blocks, _ := card["body"].([]any)
	if len(blocks) != 3 { // summary + meta + detail
		t.Fatalf("expected summary/meta/detail blocks, got %v", blocks)
	}
	first := blocks[0].(map[string]any)
	if first["color"] != "Attention" { // level error
		t.Errorf("error level should colour the title Attention, got %v", first["color"])
	}
	if !strings.Contains(first["text"].(string), "apply failed") {
		t.Errorf("title = %v", first["text"])
	}
}

// Matrix gets an m.room.message content object; the HTML body is escaped.
func TestRenderMatrix(t *testing.T) {
	ev := testEvent()
	ev.Detail = `Error: <b>bad</b> & worse`
	msg, err := Render(KindMatrix, ev)
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, msg.Body)
	if body["msgtype"] != "m.text" || body["format"] != "org.matrix.custom.html" {
		t.Fatalf("payload = %v", body)
	}
	plain, _ := body["body"].(string)
	if !strings.Contains(plain, "apply failed") || strings.Contains(plain, "*ArcLoom*") {
		t.Fatalf("plain body should be markup-free: %q", plain)
	}
	formatted, _ := body["formatted_body"].(string)
	if !strings.Contains(formatted, "<strong>ArcLoom</strong>") {
		t.Fatalf("formatted body = %q", formatted)
	}
	if strings.Contains(formatted, "<b>bad</b>") || !strings.Contains(formatted, "&lt;b&gt;bad&lt;/b&gt;") {
		t.Fatalf("detail must be HTML-escaped: %q", formatted)
	}
}

// ntfy publishes a plain-text body with the title/priority/tags in headers.
func TestRenderNtfy(t *testing.T) {
	ev := testEvent()
	ev.Project = "héllo" // non-ASCII: headers are read as latin-1 by ntfy
	msg, err := Render(KindNtfy, ev)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", msg.ContentType)
	}
	if strings.HasPrefix(string(msg.Body), "{") {
		t.Fatalf("body should be plain text, got %s", msg.Body)
	}
	if !strings.Contains(string(msg.Body), "quota exceeded") {
		t.Fatalf("body = %s", msg.Body)
	}
	if got := msg.Headers["Title"]; got != "ArcLoom: apply failed" {
		t.Fatalf("Title = %q", got)
	}
	if msg.Headers["Priority"] != "high" || msg.Headers["Tags"] != "rotating_light" {
		t.Fatalf("headers = %v", msg.Headers)
	}
	// The non-ASCII project name reaches the body, never a header.
	for k, v := range msg.Headers {
		for _, r := range v {
			if r > 0x7f {
				t.Fatalf("header %s carries non-ASCII: %q", k, v)
			}
		}
	}
	// Success is quieter than failure.
	ev.Level, ev.Summary = LevelInfo, "apply succeeded"
	msg, _ = Render(KindNtfy, ev)
	if msg.Headers["Priority"] != "low" {
		t.Fatalf("info priority = %q, want low", msg.Headers["Priority"])
	}
}

// The headers a destination needs are actually sent on the wire.
func TestSendAppliesKindHeaders(t *testing.T) {
	var got http.Header
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	if err := Send(context.Background(), Target{Kind: KindNtfy, URL: srv.URL}, testEvent()); err != nil {
		t.Fatal(err)
	}
	if got.Get("Title") != "ArcLoom: apply failed" || got.Get("Priority") != "high" {
		t.Fatalf("ntfy headers not sent: %v", got)
	}
	if got.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", got.Get("Content-Type"))
	}
	if strings.HasPrefix(string(body), "{") {
		t.Fatalf("ntfy body should be plain text: %s", body)
	}
}

// A generic webhook receives the event verbatim as JSON.
func TestSendWebhookPayload(t *testing.T) {
	var got Event
	var ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctype = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
	}))
	defer srv.Close()

	if err := Send(context.Background(), Target{Kind: KindWebhook, URL: srv.URL}, testEvent()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ctype != "application/json" {
		t.Fatalf("Content-Type = %q", ctype)
	}
	if got.Type != TypeApplyFailed || got.Project != "homelab" || got.Env != "prod" || got.User != "alice" {
		t.Fatalf("payload lost fields: %+v", got)
	}
}

// A Slack-style webhook receives a single "text" field with the summary, the
// metadata line and the detail block.
func TestSendSlackPayload(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()

	if err := Send(context.Background(), Target{Kind: KindSlack, URL: srv.URL}, testEvent()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("slack payload should carry only text, got %v", body)
	}
	text, _ := body["text"].(string)
	for _, want := range []string{"*ArcLoom*", "apply failed", "project: homelab", "environment: prod", "by: alice", "quota exceeded"} {
		if !strings.Contains(text, want) {
			t.Fatalf("slack text missing %q:\n%s", want, text)
		}
	}
}

func TestSendReportsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_token", http.StatusForbidden)
	}))
	defer srv.Close()

	err := Send(context.Background(), Target{Kind: KindSlack, URL: srv.URL}, testEvent())
	if err == nil {
		t.Fatal("expected an error for a 403 response")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error should report the status: %v", err)
	}
	// The upstream body must NOT come back: handleTestNotify returns this error
	// to the caller, and echoing what an arbitrary URL replied is what made the
	// webhook a read-anything oracle for any user with the `resources` perm.
	if strings.Contains(err.Error(), "invalid_token") {
		t.Fatalf("error leaked the upstream response body: %v", err)
	}
}

// A webhook that redirects is a misconfiguration; following it would post the
// payload somewhere the operator never configured.
func TestSendRefusesRedirects(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("payload followed a redirect")
	}))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer srv.Close()

	if err := Send(context.Background(), Target{URL: srv.URL}, testEvent()); err == nil {
		t.Fatal("expected the redirect to be refused")
	}
}

func TestSendRejectsBadURL(t *testing.T) {
	if err := Send(context.Background(), Target{URL: "file:///tmp/x"}, testEvent()); err == nil {
		t.Fatal("expected a validation error")
	}
}

// TestMain allows loopback for the whole package: httptest can only bind
// 127.0.0.1, so every delivery test would otherwise be refused by the
// destination policy. Production defaults to refusing it (see AllowLoopback).
func TestMain(m *testing.M) {
	AllowLoopback(true)
	os.Exit(m.Run())
}

// TestValidateURLRefusesMetadataAndLoopback is the SSRF boundary: a webhook is
// configurable with only the `resources` permission, so these destinations must
// be refused before the server is made to fetch them.
func TestValidateURLRefusesMetadataAndLoopback(t *testing.T) {
	AllowLoopback(false)
	defer AllowLoopback(true)

	for _, bad := range []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://[fe80::1]/x",
		"http://0.0.0.0/x",
		"http://127.0.0.1:8080/api/projects",
		"http://localhost:8080/api/projects",
		"http://[::1]/x",
	} {
		if err := ValidateURL(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	// A LAN address is the NORMAL homelab destination and must keep working.
	for _, ok := range []string{
		"http://192.168.1.50:8080/ntfy-topic",
		"http://10.0.0.5/hooks/x",
		"https://hooks.slack.com/services/T/B/x",
	} {
		if err := ValidateURL(ok); err != nil {
			t.Errorf("%s should be allowed: %v", ok, err)
		}
	}
}
