package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/notify"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// notifyServer is driftTestServer with a settings store that HAS a master key,
// since a webhook URL is stored encrypted (the shared testSettings has none).
func notifyServer(t *testing.T, fake *fakeRunner) (*Server, http.Handler) {
	t.Helper()
	srv, _ := driftTestServer(t, fake)
	set, err := settings.NewStore(t.TempDir()+"/settings.yaml", "test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	srv.settings = set
	return srv, srv.Handler()
}

// hookServer collects webhook deliveries so a test can wait for one.
type hookServer struct {
	*httptest.Server
	events chan notify.Event
	status int
}

func newHookServer(t *testing.T) *hookServer {
	t.Helper()
	h := &hookServer{events: make(chan notify.Event, 8), status: http.StatusOK}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev notify.Event
		_ = json.NewDecoder(r.Body).Decode(&ev)
		h.events <- ev
		w.WriteHeader(h.status)
	}))
	t.Cleanup(h.Close)
	return h
}

// wait returns the next delivered event, failing if none arrives (deliveries are
// dispatched in a goroutine).
func (h *hookServer) wait(t *testing.T) notify.Event {
	t.Helper()
	select {
	case ev := <-h.events:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no notification delivered")
		return notify.Event{}
	}
}

// none asserts nothing is delivered within a short window.
func (h *hookServer) none(t *testing.T) {
	t.Helper()
	select {
	case ev := <-h.events:
		t.Fatalf("unexpected notification: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestNotifyConfigNeverReturnsTheURL(t *testing.T) {
	srv, h := notifyServer(t, &fakeRunner{})

	body := `{"enabled":true,"kind":"slack","url":"https://hooks.slack.com/services/T0/B0/SECRET","onApplyFailure":true,"onDrift":true}`
	rec := post(t, h, http.MethodPut, "/api/projects/p1/notify", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT notify = %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("response leaked the webhook URL: %s", rec.Body.String())
	}

	rec = post(t, h, http.MethodGet, "/api/projects/p1/notify", "")
	var got struct{ Config notifyDTO }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Config.Enabled || got.Config.Kind != notify.KindSlack || !got.Config.OnApplyFailure || !got.Config.OnDrift {
		t.Fatalf("config not stored: %+v", got.Config)
	}
	if !got.Config.URLSet || got.Config.URLHint != "https://hooks.slack.com/…" {
		t.Fatalf("expected a masked hint, got %+v", got.Config)
	}
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("GET leaked the webhook URL: %s", rec.Body.String())
	}
	// The URL itself is readable server-side (that is how events get delivered).
	if stored, _ := srv.settings.Notify("p1"); stored.URL != "https://hooks.slack.com/services/T0/B0/SECRET" {
		t.Fatalf("stored URL = %q", stored.URL)
	}
}

// A blank URL keeps the stored one (the UI never receives it back), mirroring the
// stored git credentials.
func TestSetNotifyKeepsStoredURL(t *testing.T) {
	srv, h := notifyServer(t, &fakeRunner{})
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"kind":"webhook","url":"https://example.com/hook","onApplyFailure":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"kind":"webhook","onApplySuccess":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT without a URL = %d (%s)", rec.Code, rec.Body.String())
	}
	cfg, _ := srv.settings.Notify("p1")
	if cfg.URL != "https://example.com/hook" {
		t.Fatalf("URL not preserved: %q", cfg.URL)
	}
	if cfg.OnApplyFailure || !cfg.OnApplySuccess {
		t.Fatalf("event flags not replaced: %+v", cfg)
	}
}

func TestSetNotifyValidation(t *testing.T) {
	_, h := notifyServer(t, &fakeRunner{})
	bad := []string{
		`{"enabled":true,"url":"not-a-url"}`,                 // not http(s)
		`{"enabled":true,"url":"file:///etc/x"}`,             // scheme rejected
		`{"enabled":true}`,                                   // enabling with no URL at all
		`{"kind":"pigeon","url":"https://example.com/hook"}`, // unknown destination
	}
	for _, body := range bad {
		if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d, want 400", body, rec.Code)
		}
	}
	// Disabling without a URL is fine (mirrors drift/cost: turning it off is free).
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("disable = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := post(t, h, http.MethodPut, "/api/projects/nope/notify", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d, want 404", rec.Code)
	}
}

// Every supported destination round-trips through the config endpoint.
func TestSetNotifyAcceptsEveryDestination(t *testing.T) {
	srv, h := notifyServer(t, &fakeRunner{})
	for _, kind := range []string{
		notify.KindWebhook, notify.KindSlack, notify.KindDiscord,
		notify.KindFluxer, notify.KindTeams, notify.KindMatrix, notify.KindNtfy,
	} {
		body := `{"enabled":true,"kind":"` + kind + `","url":"https://example.com/hook","onApplyFailure":true}`
		rec := post(t, h, http.MethodPut, "/api/projects/p1/notify", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT kind=%s = %d (%s)", kind, rec.Code, rec.Body.String())
		}
		if cfg, _ := srv.settings.Notify("p1"); cfg.Kind != kind {
			t.Fatalf("stored kind = %q, want %q", cfg.Kind, kind)
		}
	}
}

// Without ARCLOOM_SECRET_KEY the URL cannot be encrypted at rest, so saving one
// fails with an actionable message instead of storing it in the clear.
func TestSetNotifyRequiresSecretKey(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{}) // testSettings: no master key
	h := srv.Handler()
	rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"url":"https://example.com/hook"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ARCLOOM_SECRET_KEY") {
		t.Fatalf("expected an actionable message, got %s", rec.Body.String())
	}
}

// The test endpoint delivers through the stored config and reports the outcome —
// it works while notifications are disabled so a setup can be verified first.
func TestNotifyTestEndpoint(t *testing.T) {
	_, h := notifyServer(t, &fakeRunner{})
	hook := newHookServer(t)

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":false,"kind":"webhook","url":"`+hook.URL+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := post(t, h, http.MethodPost, "/api/projects/p1/notify/test", ""); rec.Code != http.StatusOK {
		t.Fatalf("test = %d (%s)", rec.Code, rec.Body.String())
	}
	if ev := hook.wait(t); ev.Type != notify.TypeTest {
		t.Fatalf("event type = %q", ev.Type)
	}

	// A webhook that rejects the delivery surfaces as a 502, not a silent success.
	hook.status = http.StatusForbidden
	if rec := post(t, h, http.MethodPost, "/api/projects/p1/notify/test", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("failing webhook = %d, want 502", rec.Code)
	}
	hook.wait(t)
}

func TestNotifyTestRequiresStoredURL(t *testing.T) {
	_, h := notifyServer(t, &fakeRunner{})
	if rec := post(t, h, http.MethodPost, "/api/projects/p1/notify/test", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("test without a URL = %d, want 400", rec.Code)
	}
}

// A failed apply notifies through the real streamOp path.
func TestApplyFailureNotifies(t *testing.T) {
	fake := &fakeRunner{applyErr: errors.New("Error: quota exceeded\nsecond line")}
	_, h := notifyServer(t, fake)
	hook := newHookServer(t)

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"kind":"webhook","url":"`+hook.URL+`","onApplyFailure":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	post(t, h, http.MethodPost, "/api/projects/p1/apply", "")

	ev := hook.wait(t)
	if ev.Type != notify.TypeApplyFailed || ev.Level != notify.LevelError {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Project != "demo" || ev.ProjectID != "p1" || ev.Op != "apply" {
		t.Fatalf("event lost context: %+v", ev)
	}
	if ev.Detail != "Error: quota exceeded" {
		t.Fatalf("detail = %q, want the first error line", ev.Detail)
	}
}

// A successful apply is silent unless success notifications are requested.
func TestApplySuccessRespectsEventFlags(t *testing.T) {
	_, h := notifyServer(t, &fakeRunner{})
	hook := newHookServer(t)

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"kind":"webhook","url":"`+hook.URL+`","onApplyFailure":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	post(t, h, http.MethodPost, "/api/projects/p1/apply", "")
	hook.none(t)

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"kind":"webhook","onApplySuccess":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	post(t, h, http.MethodPost, "/api/projects/p1/apply", "")
	if ev := hook.wait(t); ev.Type != notify.TypeApplySucceeded {
		t.Fatalf("event = %+v", ev)
	}
}

// Read-only operations (plan/validate/lint) are never notified: the user is
// already watching the stream.
func TestPlanDoesNotNotify(t *testing.T) {
	srv, h := notifyServer(t, &fakeRunner{})
	hook := newHookServer(t)
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"kind":"webhook","url":"`+hook.URL+`","onApplyFailure":true,"onApplySuccess":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	post(t, h, http.MethodGet, "/api/projects/p1/plan", "")
	hook.none(t)

	// Same for an op that is not a deploy at all.
	p, _ := srv.store.Get("p1")
	srv.notifyRun(p, &runs.Run{ProjectID: "p1", Op: "state-rm", Status: "error", Error: "boom"})
	hook.none(t)
}

// A scheduled drift check notifies when the outcome changes — and only then, so a
// steadily drifted project does not report every interval.
func TestDriftNotifiesOnChange(t *testing.T) {
	fake := &fakeRunner{res: runner.PlanResult{HasChanges: true, Change: 2}}
	srv, h := notifyServer(t, fake)
	hook := newHookServer(t)
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/notify",
		`{"enabled":true,"kind":"webhook","url":"`+hook.URL+`","onDrift":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}

	srv.runDriftCheck(context.Background(), "p1")
	ev := hook.wait(t)
	if ev.Type != notify.TypeDriftDetected || ev.Level != notify.LevelWarn {
		t.Fatalf("event = %+v", ev)
	}
	if !strings.Contains(ev.Detail, "2 to change") {
		t.Fatalf("detail = %q", ev.Detail)
	}

	// Unchanged outcome: silent.
	srv.runDriftCheck(context.Background(), "p1")
	hook.none(t)

	// Drift resolved: reported once.
	fake.res = runner.PlanResult{}
	srv.runDriftCheck(context.Background(), "p1")
	if ev := hook.wait(t); ev.Type != notify.TypeDriftResolved {
		t.Fatalf("event = %+v", ev)
	}
}
