package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// RotateReport summarizes one store's master-key rotation. It is shared with
// internal/settings, which rotates the same passphrase over a different set of
// sealed items, so the two halves of a rotation report the same shape.
type RotateReport struct {
	// Items is how many sealed documents the rotation examined: one .enc file
	// per project here, one encrypted field in the settings document there.
	Items int `json:"items"`
	// Rekeyed is how many were re-sealed under the new key. An item already
	// under it (a resumed rotation) counts in Verified but not here.
	Rekeyed int `json:"rekeyed"`
	// Verified is how many read back cleanly under the NEW key alone — the
	// proof that the rotation actually took, rather than the claim that it did.
	Verified int `json:"verified"`
	// Values is the number of individual secret values that verified (secrets
	// store only; a settings field holds exactly one value).
	Values int `json:"values"`
	// Unreadable names the items that could not be read. Non-empty means the
	// rotation is INCOMPLETE and the old key material is still needed.
	Unreadable []string `json:"unreadable,omitempty"`
	// Complete is true only when every item verified under the new key.
	Complete bool `json:"complete"`
}

// tryDecrypt opens data with the first key that works. Keys are tried in order,
// nil entries skipped.
func tryDecrypt(data []byte, keys [][]byte) ([]byte, bool) {
	for _, k := range keys {
		if k == nil {
			continue
		}
		if plain, err := decrypt(k, data); err == nil {
			return plain, true
		}
	}
	return nil, false
}

// decryptKeys is the ordered set of keys a stored blob may be sealed under: the
// current one, the previous generation's (an unfinished rotation) and the
// pre-KDF sha256 one (an unfinished legacy migration).
func (s *Store) decryptKeys() [][]byte {
	return [][]byte{s.key, s.prevKey, s.legacyKey}
}

// encFiles lists the project ids that have a local secret document.
func (s *Store) encFiles() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".enc") {
			continue
		}
		if id := strings.TrimSuffix(e.Name(), ".enc"); validProjectID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// LocalStatus reports what the local encrypted store holds and what the current
// key can actually open. It exists because a failed decrypt and an absent value
// otherwise look identical from the outside: without it a rotation could not
// verify itself, and a wrong ARCLOOM_SECRET_KEY would surface only later, as a
// plan failing on a missing variable.
func (s *Store) LocalStatus() (files int, values int, unreadable []string) {
	if s.dir == "" || s.key == nil {
		return 0, 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.encFiles()
	if err != nil {
		return 0, 0, nil
	}
	keys := s.decryptKeys()
	for _, id := range ids {
		files++
		data, err := os.ReadFile(s.path(id))
		if err != nil {
			unreadable = append(unreadable, id)
			continue
		}
		plain, ok := tryDecrypt(data, keys)
		if !ok {
			unreadable = append(unreadable, id)
			continue
		}
		var m map[string]string
		if json.Unmarshal(plain, &m) != nil {
			unreadable = append(unreadable, id)
			continue
		}
		values += len(m)
	}
	return files, values, unreadable
}

// Rotate re-seals every local secret document from oldMaster to newMaster and
// proves the result: it decrypts everything with the old key BEFORE writing
// anything (so a rotation started on a guessed passphrase cannot seal the
// unreadable half under a key nobody has), re-encrypts each file through a
// temp-then-rename, then reads every file back under the NEW key alone. Only
// when that verify pass is clean is the rotation reported complete and the old
// key material dropped.
//
// It is RESUMABLE: the new generation's salt is recorded (with the previous
// one, marked in progress) before the first file is rewritten, and a file
// already under the new key is accepted, so re-running with the same old/new
// pair finishes an interrupted rotation instead of starting a third generation.
//
// Only the LOCAL files are touched. An external secret store holds its values
// under its own credentials; this key never sealed them.
func (s *Store) Rotate(oldMaster, newMaster string) (RotateReport, error) {
	var rep RotateReport
	if newMaster == "" {
		return rep, errors.New("the new key must not be empty")
	}
	if oldMaster == newMaster {
		return rep, errors.New("the new key must differ from the current one")
	}
	if s.dir == "" {
		return rep, errors.New("no secrets directory configured")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	meta, _, err := readKDF(s.dir)
	if err != nil {
		return rep, err
	}
	// Salt of the generation being written. An interrupted rotation already
	// installed one; reuse it so a re-run FINISHES that rotation rather than
	// starting a third generation the half-rewritten files know nothing about.
	newSalt, prevSalt := meta.Salt, meta.PrevSalt
	if !meta.Rotating {
		prevSalt = meta.Salt
		if newSalt, err = NewSalt(); err != nil {
			return rep, err
		}
	}
	newKey := DeriveKey(newMaster, newSalt)
	// What an existing blob may be sealed under: the previous argon2id
	// generation, or the pre-KDF sha256 key of the same passphrase.
	oldKey := DeriveKey(oldMaster, prevSalt)
	if len(prevSalt) == 0 {
		oldKey = nil
	}
	oldLegacy := LegacyKey(oldMaster)

	ids, err := s.encFiles()
	if err != nil {
		return rep, err
	}
	rep.Items = len(ids)

	// Pass 1 — prove the supplied current key reads everything before a single
	// byte is written. Nothing is mutated if any file fails here.
	type staged struct {
		plain   []byte
		current bool // already sealed under the new key (a resumed rotation)
	}
	blobs := make(map[string]staged, len(ids))
	for _, id := range ids {
		data, err := os.ReadFile(s.path(id))
		if err != nil {
			return rep, fmt.Errorf("read %s: %w", id, err)
		}
		if plain, err := decrypt(newKey, data); err == nil {
			blobs[id] = staged{plain: plain, current: true}
			continue
		}
		plain, ok := tryDecrypt(data, [][]byte{oldKey, oldLegacy})
		if !ok {
			rep.Unreadable = append(rep.Unreadable, id)
			continue
		}
		blobs[id] = staged{plain: plain}
	}
	if len(rep.Unreadable) > 0 {
		return rep, fmt.Errorf("the current key does not decrypt %d secret file(s): %s",
			len(rep.Unreadable), strings.Join(rep.Unreadable, ", "))
	}

	// Record the new generation BEFORE re-sealing anything, marked in progress:
	// a crash from here on leaves a store whose salts identify both generations,
	// which is what lets a re-run finish the job.
	if err := writeKDF(s.dir, kdfMeta{Version: 1, Salt: newSalt, PrevSalt: prevSalt, Rotating: true}); err != nil {
		return rep, fmt.Errorf("record key rotation: %w", err)
	}
	// From here the store writes under the new key and keeps the old one as a
	// read fallback, so a concurrent reader sees no gap.
	s.key, s.prevKey = newKey, oldKey

	for _, id := range ids {
		b := blobs[id]
		if b.current {
			continue // already rotated by an earlier, interrupted run
		}
		enc, err := encrypt(newKey, b.plain)
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, id)
			continue
		}
		if err := writeFileAtomic(s.path(id), enc, 0o600); err != nil {
			rep.Unreadable = append(rep.Unreadable, id)
			continue
		}
		rep.Rekeyed++
	}

	// Pass 2 — read everything back under the NEW key alone. A file that needs
	// a fallback has not really been rotated.
	rep.Unreadable = nil
	for _, id := range ids {
		data, err := os.ReadFile(s.path(id))
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, id)
			continue
		}
		plain, err := decrypt(newKey, data)
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, id)
			continue
		}
		var m map[string]string
		if err := json.Unmarshal(plain, &m); err != nil {
			rep.Unreadable = append(rep.Unreadable, id)
			continue
		}
		rep.Verified++
		rep.Values += len(m)
	}
	rep.Complete = len(rep.Unreadable) == 0
	if !rep.Complete {
		return rep, fmt.Errorf("rotation incomplete: %d file(s) could not be read back under the new key (%s)",
			len(rep.Unreadable), strings.Join(rep.Unreadable, ", "))
	}
	// Nothing remains under the old key: drop the fallbacks and clear the
	// in-progress marker, so the next start needs only the new passphrase.
	s.prevKey, s.legacyKey = nil, nil
	if err := writeKDF(s.dir, kdfMeta{Version: 1, Salt: newSalt}); err != nil {
		return rep, fmt.Errorf("finalize key rotation: %w", err)
	}
	return rep, nil
}
