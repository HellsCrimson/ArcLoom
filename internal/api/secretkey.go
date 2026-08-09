package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// secretKeyDTO is what the Danger-zone panel needs to describe the blast radius
// BEFORE anything is touched: what the key seals, and what it currently opens.
//
// The unreadable counts are the reason this endpoint exists. Until now a field
// sealed under a key the process no longer has decrypted to "" — indistinguish-
// able from "never configured" — so a mistyped ARCLOOM_SECRET_KEY surfaced days
// later as an SSO login rejected on a wrong client secret.
type secretKeyDTO struct {
	// KeySet reports whether ARCLOOM_SECRET_KEY is configured at all; without
	// it nothing is sealed and there is nothing to rotate.
	KeySet bool `json:"keySet"`
	// SecretFiles / SecretValues describe the local encrypted secret store.
	SecretFiles  int `json:"secretFiles"`
	SecretValues int `json:"secretValues"`
	// UnreadableSecrets names project ids whose .enc file no available key opens.
	UnreadableSecrets []string `json:"unreadableSecrets,omitempty"`
	// SettingsFields is every at-rest encrypted settings field, each flagged
	// readable or not (SSO client secret, git credentials, notify URL, external
	// store token).
	SettingsFields []settings.FieldStatus `json:"settingsFields"`
	// UnreadableFields counts the entries above that fail to decrypt.
	UnreadableFields int `json:"unreadableFields"`
	// ExternalStore names the external secret backend when one is active. Its
	// VALUES are not sealed with this key — only the token used to reach it.
	ExternalStore string `json:"externalStore,omitempty"`
	// Busy is true while an engine run (plan/apply/state/drift) is in flight; a
	// rotation refuses to start then.
	Busy bool `json:"busy"`
}

func (s *Server) secretKeyResponse() secretKeyDTO {
	dto := secretKeyDTO{
		KeySet: s.cfg.SecretKey != "",
		Busy:   s.engineBusy(),
	}
	if s.secrets != nil {
		dto.SecretFiles, dto.SecretValues, dto.UnreadableSecrets = s.secrets.LocalStatus()
		if r := s.secrets.Remote(); r != nil {
			dto.ExternalStore = r.Name()
		}
	}
	dto.SettingsFields = s.settings.EncryptedFields()
	for _, f := range dto.SettingsFields {
		if !f.Readable {
			dto.UnreadableFields++
		}
	}
	return dto
}

// handleGetSecretKey reports what the master key seals and what it can open.
// Admin only; it exposes counts and field NAMES, never a value.
func (s *Server) handleGetSecretKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.secretKeyResponse())
}

type rotateKeyRequest struct {
	// CurrentKey must equal the live ARCLOOM_SECRET_KEY. Requiring it means a
	// rotation is never started on a guess — the one operation here that can
	// make credentials unrecoverable.
	CurrentKey string `json:"currentKey"`
	NewKey     string `json:"newKey"`
}

// rotateKeyResponse reports both halves separately (they are independent stores
// with independent salts) plus the status the panel re-renders from.
type rotateKeyResponse struct {
	Secrets  secrets.RotateReport `json:"secrets"`
	Settings secrets.RotateReport `json:"settings"`
	// Complete is true only when BOTH halves verified under the new key.
	Complete bool `json:"complete"`
	// Warnings carry the things an operator must act on — above all that the
	// process now uses a key the environment does not yet hold.
	Warnings []string     `json:"warnings,omitempty"`
	Status   secretKeyDTO `json:"status"`
}

// handleRotateSecretKey re-keys both encrypted stores — the per-project secret
// files and the settings document's *_enc fields — in one operation, then
// proves it worked by reading everything back under the new key.
//
// Admin only, CSRF-guarded, audited. It refuses while an engine run is in
// flight: a plan holds resolved secrets and the workspace lock, and re-sealing
// the store underneath it is exactly the race worth not having.
//
// The process switches to the new key immediately; the ENVIRONMENT does not.
// Until ARCLOOM_SECRET_KEY is updated, a restart would come back with a key
// that opens nothing — so that window is reported as a warning rather than left
// for the operator to discover.
func (s *Server) handleRotateSecretKey(w http.ResponseWriter, r *http.Request) {
	if !s.csrfSafe(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req rotateKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if s.cfg.SecretKey == "" {
		writeErr(w, http.StatusBadRequest,
			"no ARCLOOM_SECRET_KEY is configured, so nothing is encrypted with one")
		return
	}
	// Never rotate on a guess: the supplied current key must be the live one.
	if req.CurrentKey != s.cfg.SecretKey {
		writeErr(w, http.StatusBadRequest, "the current key is wrong")
		return
	}
	// Same rule the server applies to ARCLOOM_SECRET_KEY at boot, from the same
	// definition: rotating TO a key the next start would refuse would leave the
	// instance unbootable with everything sealed under it.
	if err := secrets.ValidateMasterKey(req.NewKey); err != nil {
		writeErr(w, http.StatusBadRequest, "the new key is not usable: "+err.Error())
		return
	}
	if req.NewKey == req.CurrentKey {
		writeErr(w, http.StatusBadRequest, "the new key must differ from the current one")
		return
	}
	if s.engineBusy() {
		writeErr(w, http.StatusConflict,
			"a plan, apply or drift check is running; wait for it to finish before rotating the key")
		return
	}

	resp := rotateKeyResponse{}
	var failed error
	if s.secrets != nil {
		rep, err := s.secrets.Rotate(req.CurrentKey, req.NewKey)
		resp.Secrets = rep
		if err != nil {
			failed = err
		}
	} else {
		resp.Secrets.Complete = true
	}
	// The settings half runs even when the secrets half failed: its fields are
	// sealed independently, and leaving them under the old key while the
	// process moved on would be the worse outcome.
	setRep, setErr := s.settings.Rotate(req.CurrentKey, req.NewKey)
	resp.Settings = setRep
	if setErr != nil && failed == nil {
		failed = setErr
	}

	resp.Complete = resp.Secrets.Complete && resp.Settings.Complete
	if resp.Complete {
		// The running process now seals under the new key; keep the in-memory
		// config in step so a second rotation validates against it.
		s.cfg.SecretKey = req.NewKey
		resp.Warnings = append(resp.Warnings,
			"Update ARCLOOM_SECRET_KEY in the server's environment to the new key BEFORE restarting — a restart with the old key opens nothing.")
		slog.Warn("master key rotated; update ARCLOOM_SECRET_KEY in the environment before restarting",
			"secretFiles", resp.Secrets.Verified, "settingsFields", resp.Settings.Verified)
	}
	resp.Status = s.secretKeyResponse()
	if resp.Complete && resp.Status.ExternalStore != "" {
		resp.Warnings = append(resp.Warnings,
			"Secrets held in "+resp.Status.ExternalStore+" were not touched; only its access token is sealed with this key.")
	}

	if failed != nil {
		// Partial: report what did land alongside the failure, so the operator
		// knows whether to re-run (resumable) or restore a backup.
		writeJSON(w, http.StatusInternalServerError, struct {
			rotateKeyResponse
			Error string `json:"error"`
		}{resp, failed.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
