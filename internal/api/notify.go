package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/notify"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// notifyTimeout bounds one delivery attempt. Events fire after the operation
// they describe has finished, so they run on their own context (the request's is
// already cancelled by the time the SSE stream closes).
const notifyTimeout = 20 * time.Second

// notifyDTO is the client-facing view of a project's notification config. The
// webhook URL is never returned — it is a credential (a Slack incoming-webhook
// URL is bearer-equivalent) — only whether one is stored plus a masked hint, the
// same contract as the stored git credentials.
type notifyDTO struct {
	Enabled        bool   `json:"enabled"`
	Kind           string `json:"kind"`
	URLSet         bool   `json:"urlSet"`
	URLHint        string `json:"urlHint,omitempty"`
	OnApplyFailure bool   `json:"onApplyFailure"`
	OnApplySuccess bool   `json:"onApplySuccess"`
	OnDrift        bool   `json:"onDrift"`
	// Storable reports whether a URL can be saved at all (a master key is
	// configured), so the UI can explain the ARCLOOM_SECRET_KEY requirement.
	Storable bool `json:"storable"`
}

// notifySetRequest is the editable shape. An empty URL keeps the stored one.
type notifySetRequest struct {
	Enabled        bool   `json:"enabled"`
	Kind           string `json:"kind"`
	URL            string `json:"url"`
	OnApplyFailure bool   `json:"onApplyFailure"`
	OnApplySuccess bool   `json:"onApplySuccess"`
	OnDrift        bool   `json:"onDrift"`
}

// notifyConfig returns a project's stored notification config (zero value when
// nothing is stored, or the settings store is absent as in tests).
func (s *Server) notifyConfig(projectID string) settings.Notify {
	cfg, _ := s.storedNotify(projectID)
	return cfg
}

// storedNotify reports the config plus whether the project has ever been
// configured, so the UI can be shown sensible defaults for a fresh project
// instead of "notify on nothing".
func (s *Server) storedNotify(projectID string) (settings.Notify, bool) {
	if s.settings == nil {
		return settings.Notify{}, false
	}
	return s.settings.Notify(projectID)
}

func (s *Server) notifyDTO(projectID string) notifyDTO {
	cfg, configured := s.storedNotify(projectID)
	if !configured {
		// Defaults for a project nobody has set up yet: the two events worth
		// waking somebody for. Nothing is sent until a URL is saved anyway.
		cfg.OnApplyFailure, cfg.OnDrift = true, true
	}
	return notifyDTO{
		Enabled:        cfg.Enabled,
		Kind:           notify.NormalizeKind(cfg.Kind),
		URLSet:         cfg.URL != "",
		URLHint:        notify.MaskURL(cfg.URL),
		OnApplyFailure: cfg.OnApplyFailure,
		OnApplySuccess: cfg.OnApplySuccess,
		OnDrift:        cfg.OnDrift,
		Storable:       s.settings != nil && s.settings.SecretStorable(),
	}
}

// handleGetNotify returns a project's notification config (any authenticated
// project reader; the URL itself is withheld).
func (s *Server) handleGetNotify(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": s.notifyDTO(id)})
}

// handleSetNotify saves a project's notification config (resources perm). Not
// license-gated: notifications are community/Apache core like the drift status
// they report on.
func (s *Server) handleSetNotify(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if s.settings == nil {
		writeErr(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	var req notifySetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Kind != "" && !notify.ValidKind(req.Kind) {
		writeErr(w, http.StatusBadRequest, "unknown notification destination: "+req.Kind)
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL != "" {
		if err := notify.ValidateURL(req.URL); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// Enabling without a URL (neither sent now nor stored before) would silently
	// notify nobody.
	if stored, _ := s.settings.Notify(id); req.Enabled && req.URL == "" && stored.URL == "" {
		writeErr(w, http.StatusBadRequest, "a webhook URL is required to enable notifications")
		return
	}
	err := s.settings.SetNotify(id, settings.Notify{
		Enabled:        req.Enabled,
		Kind:           notify.NormalizeKind(req.Kind),
		URL:            req.URL,
		OnApplyFailure: req.OnApplyFailure,
		OnApplySuccess: req.OnApplySuccess,
		OnDrift:        req.OnDrift,
	})
	if errors.Is(err, settings.ErrSecretKeyRequired) {
		writeErr(w, http.StatusBadRequest, "set ARCLOOM_SECRET_KEY on the server to store a webhook URL")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": s.notifyDTO(id)})
}

// handleTestNotify delivers a test event through the stored config so an
// operator can confirm the webhook works before waiting for a real failure. It
// runs synchronously (unlike real events) and reports the delivery error, and it
// works while notifications are disabled so a setup can be verified first.
func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.store.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	cfg := s.notifyConfig(id)
	if cfg.URL == "" {
		writeErr(w, http.StatusBadRequest, "save a webhook URL first")
		return
	}
	ev := notify.Event{
		Type:      notify.TypeTest,
		Level:     notify.LevelInfo,
		Project:   p.Name,
		ProjectID: p.ID,
		Summary:   "test notification",
		Detail:    "If you can read this, ArcLoom can reach your webhook.",
		At:        time.Now(),
	}
	if u, ok := userFromContext(r); ok {
		ev.User = u.Username
	}
	ctx, cancel := context.WithTimeout(r.Context(), notifyTimeout)
	defer cancel()
	if err := notify.Send(ctx, notify.Target{Kind: cfg.Kind, URL: cfg.URL}, ev); err != nil {
		writeErr(w, http.StatusBadGateway, "delivery failed: "+firstLine(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// dispatchNotify delivers an event in the background. Failures are logged, never
// surfaced: the operation the event describes has already completed.
func (s *Server) dispatchNotify(cfg settings.Notify, ev notify.Event) {
	if !cfg.Notified() {
		return
	}
	target := notify.Target{Kind: cfg.Kind, URL: cfg.URL}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		if err := notify.Send(ctx, target, ev); err != nil {
			slog.Warn("notification delivery failed", "project", ev.ProjectID, "event", ev.Type, "err", err)
		}
	}()
}

// notifyRun fires a notification for a finished deploy operation. Only apply and
// destroy qualify: a plan/validate/lint is a read-only inspection the user is
// already watching, while an apply outcome is the thing worth waking somebody
// for. A policy-blocked or approval-blocked apply arrives here as an error, so it
// is covered by the failure event.
func (s *Server) notifyRun(p *model.Project, rec *runs.Run) {
	if rec.Op != "apply" && rec.Op != "destroy" {
		return
	}
	cfg := s.notifyConfig(p.ID)
	if !cfg.Notified() {
		return
	}
	failed := rec.Status != "success"
	if failed && !cfg.OnApplyFailure {
		return
	}
	if !failed && !cfg.OnApplySuccess {
		return
	}
	ev := notify.Event{
		Project:   p.Name,
		ProjectID: p.ID,
		Env:       rec.Env,
		Op:        rec.Op,
		User:      rec.User,
		At:        rec.FinishedAt,
	}
	if failed {
		ev.Type, ev.Level = notify.TypeApplyFailed, notify.LevelError
		ev.Summary = rec.Op + " failed"
		ev.Detail = firstLine(rec.Error)
	} else {
		ev.Type, ev.Level = notify.TypeApplySucceeded, notify.LevelInfo
		ev.Summary = rec.Op + " succeeded"
		ev.Detail = fmt.Sprintf("finished in %s", rec.FinishedAt.Sub(rec.StartedAt).Round(time.Second))
	}
	s.dispatchNotify(cfg, ev)
}

// notifyDrift fires a notification for a scheduled drift check whose outcome
// changed. It is called from the same place as the run record, so a steadily
// drifted project reports once rather than every interval.
func (s *Server) notifyDrift(p *model.Project, cur driftStatus) {
	cfg := s.notifyConfig(p.ID)
	if !cfg.Notified() || !cfg.OnDrift {
		return
	}
	ev := notify.Event{
		Project:   p.Name,
		ProjectID: p.ID,
		Op:        "drift",
		At:        cur.CheckedAt,
	}
	switch {
	case cur.Error != "":
		ev.Type, ev.Level = notify.TypeDriftError, notify.LevelError
		ev.Summary = "drift check failed"
		ev.Detail = firstLine(cur.Error)
	case cur.Drifted:
		ev.Type, ev.Level = notify.TypeDriftDetected, notify.LevelWarn
		ev.Summary = "drift detected"
		ev.Detail = fmt.Sprintf("%d to add, %d to change, %d to destroy, %d to replace",
			cur.Add, cur.Change, cur.Destroy, cur.Replace)
		// Name a few of them: a bare count says nothing about what moved.
		if len(cur.Addresses) > 0 {
			shown := cur.Addresses
			if len(shown) > 5 {
				shown = shown[:5]
			}
			ev.Detail += "\n" + strings.Join(shown, "\n")
			if len(cur.Addresses) > len(shown) {
				ev.Detail += fmt.Sprintf("\n… and %d more", cur.Change+cur.Add+cur.Destroy+cur.Replace-len(shown))
			}
		}
	default:
		ev.Type, ev.Level = notify.TypeDriftResolved, notify.LevelInfo
		ev.Summary = "drift resolved — infrastructure matches state again"
	}
	s.dispatchNotify(cfg, ev)
}
