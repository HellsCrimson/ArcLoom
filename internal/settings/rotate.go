package settings

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/HellsCrimson/ArcLoom/internal/secrets"
)

// encField is one at-rest encrypted field in the settings document, with a
// stable name for reports and accessors so key rotation, the legacy migration
// and the health check all walk exactly the same list — a field missing from
// here would silently keep its old sealing key.
type encField struct {
	name string
	get  func(*document) string
	set  func(*document, string)
}

// encFields enumerates every encrypted field currently present in the document.
// Fields keyed by project are listed in a stable (sorted) order so two reports
// of the same document read the same.
func encFields(d *document) []encField {
	fields := []encField{
		{
			name: "sso.client_secret",
			get:  func(d *document) string { return d.SSO.ClientSecretEnc },
			set:  func(d *document, v string) { d.SSO.ClientSecretEnc = v },
		},
		{
			name: "secret_store.token",
			get:  func(d *document) string { return d.SecretStore.TokenEnc },
			set:  func(d *document, v string) { d.SecretStore.TokenEnc = v },
		},
	}
	for _, id := range sortedKeys(d.GitCreds) {
		fields = append(fields,
			encField{
				name: "git_creds." + id + ".token",
				get:  func(d *document) string { return d.GitCreds[id].TokenEnc },
				set:  func(d *document, v string) { c := d.GitCreds[id]; c.TokenEnc = v; d.GitCreds[id] = c },
			},
			encField{
				name: "git_creds." + id + ".ssh_key",
				get:  func(d *document) string { return d.GitCreds[id].SSHKeyEnc },
				set:  func(d *document, v string) { c := d.GitCreds[id]; c.SSHKeyEnc = v; d.GitCreds[id] = c },
			},
			encField{
				name: "git_creds." + id + ".passphrase",
				get:  func(d *document) string { return d.GitCreds[id].PassphraseEnc },
				set:  func(d *document, v string) { c := d.GitCreds[id]; c.PassphraseEnc = v; d.GitCreds[id] = c },
			},
		)
	}
	for _, id := range sortedKeys(d.Notify) {
		fields = append(fields, encField{
			name: "notify." + id + ".url",
			get:  func(d *document) string { return d.Notify[id].URLEnc },
			set:  func(d *document, v string) { n := d.Notify[id]; n.URLEnc = v; d.Notify[id] = n },
		})
	}
	// Only fields that actually hold something are interesting to any caller.
	out := fields[:0]
	for _, f := range fields {
		if f.get(d) != "" {
			out = append(out, f)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// FieldStatus reports whether one encrypted settings field can be opened with
// the key currently in use.
type FieldStatus struct {
	Field    string `json:"field"`
	Readable bool   `json:"readable"`
}

// EncryptedFields reports every at-rest secret in the document and whether the
// current key reads it. This is the settings half of making a failed decrypt
// distinguishable from an absent value: decryptSecret returns "" for both, so
// without this a wrong ARCLOOM_SECRET_KEY looks exactly like "no SSO secret
// configured" until an SSO login or a Vault call fails much later.
func (s *Store) EncryptedFields() []FieldStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	fields := encFields(&s.doc)
	out := make([]FieldStatus, 0, len(fields))
	for _, f := range fields {
		_, err := s.openSecret(f.get(&s.doc))
		out = append(out, FieldStatus{Field: f.name, Readable: err == nil})
	}
	return out
}

// Rotate re-seals every encrypted settings field from oldMaster to newMaster.
// Like the secrets store it decrypts everything first (a field the old key
// cannot open aborts the rotation before anything is written) and verifies the
// result under the NEW key alone before declaring success.
//
// Unlike the secrets store there is no in-progress marker: the whole document —
// fields and salt together — is persisted in a single atomic write, so it is
// either entirely rotated or entirely untouched.
func (s *Store) Rotate(oldMaster, newMaster string) (secrets.RotateReport, error) {
	var rep secrets.RotateReport
	if newMaster == "" {
		return rep, errors.New("the new key must not be empty")
	}
	if oldMaster == newMaster {
		return rep, errors.New("the new key must differ from the current one")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var prevSalt []byte
	if s.doc.KDF != nil {
		prevSalt = s.doc.KDF.Salt
	}
	newSalt, err := secrets.NewSalt()
	if err != nil {
		return rep, err
	}
	newKey := secrets.DeriveKey(newMaster, newSalt)
	oldKey := secrets.DeriveKey(oldMaster, prevSalt)
	if len(prevSalt) == 0 {
		oldKey = nil
	}
	oldLegacy := secrets.LegacyKey(oldMaster)

	fields := encFields(&s.doc)
	rep.Items = len(fields)

	// Pass 1 — open every field with the old key material before writing.
	plains := make(map[string][]byte, len(fields))
	for _, f := range fields {
		blob, err := base64.StdEncoding.DecodeString(f.get(&s.doc))
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, f.name)
			continue
		}
		plain, ok := openWith(blob, oldKey, oldLegacy)
		if !ok {
			rep.Unreadable = append(rep.Unreadable, f.name)
			continue
		}
		plains[f.name] = plain
	}
	if len(rep.Unreadable) > 0 {
		return rep, fmt.Errorf("the current key does not decrypt %d settings field(s): %s",
			len(rep.Unreadable), strings.Join(rep.Unreadable, ", "))
	}

	// Re-seal into a copy, so a failure anywhere leaves the live document — and
	// the file on disk — exactly as it was.
	next := s.doc.clone()
	for _, f := range fields {
		blob, err := secrets.Encrypt(newKey, plains[f.name])
		if err != nil {
			return rep, fmt.Errorf("re-encrypt %s: %w", f.name, err)
		}
		f.set(&next, base64.StdEncoding.EncodeToString(blob))
		rep.Rekeyed++
	}
	next.KDF = &kdfMeta{Version: 1, Salt: newSalt}

	prevDoc, prevKey, prevLegacy := s.doc, s.key, s.legacyKey
	s.doc, s.key, s.legacyKey = next, newKey, nil
	if err := s.persist(); err != nil {
		s.doc, s.key, s.legacyKey = prevDoc, prevKey, prevLegacy
		return rep, fmt.Errorf("save re-keyed settings: %w", err)
	}

	// Pass 2 — every field must now open under the new key alone.
	rep.Unreadable = nil
	for _, f := range fields {
		if _, err := s.openSecret(f.get(&s.doc)); err != nil {
			rep.Unreadable = append(rep.Unreadable, f.name)
			continue
		}
		rep.Verified++
		rep.Values++
	}
	rep.Complete = len(rep.Unreadable) == 0
	if !rep.Complete {
		return rep, fmt.Errorf("rotation incomplete: %d settings field(s) could not be read back under the new key (%s)",
			len(rep.Unreadable), strings.Join(rep.Unreadable, ", "))
	}
	return rep, nil
}

// openWith decrypts a blob with the first key that works.
func openWith(blob []byte, keys ...[]byte) ([]byte, bool) {
	for _, k := range keys {
		if k == nil {
			continue
		}
		if plain, err := secrets.Decrypt(k, blob); err == nil {
			return plain, true
		}
	}
	return nil, false
}

// clone deep-copies the maps a rotation rewrites, so mutating the copy cannot
// reach through into the live document (map values are shared by reference).
func (d document) clone() document {
	out := d
	if d.GitCreds != nil {
		out.GitCreds = make(map[string]GitCred, len(d.GitCreds))
		for k, v := range d.GitCreds {
			out.GitCreds[k] = v
		}
	}
	if d.Notify != nil {
		out.Notify = make(map[string]Notify, len(d.Notify))
		for k, v := range d.Notify {
			out.Notify[k] = v
		}
	}
	return out
}
