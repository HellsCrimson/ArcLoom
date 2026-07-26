package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRotate_ReKeysAndVerifies(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "old-master-key")
	if err := s.Set("proj1", "db_pw", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("proj2", "token", "abc"); err != nil {
		t.Fatal(err)
	}

	rep, err := s.Rotate("old-master-key", "new-master-key")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if !rep.Complete || rep.Items != 2 || rep.Rekeyed != 2 || rep.Verified != 2 || rep.Values != 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}

	// The rotating store keeps serving the same plaintext.
	if v, ok, err := s.Resolve("proj1", "db_pw"); err != nil || !ok || v != "s3cr3t" {
		t.Fatalf("resolve after rotate: %q %v %v", v, ok, err)
	}
	// A fresh process with the NEW key reads everything.
	if v, ok, err := NewStore(dir, "new-master-key").Resolve("proj2", "token"); err != nil || !ok || v != "abc" {
		t.Fatalf("resolve with new key: %q %v %v", v, ok, err)
	}
	// ...and zero blobs remain readable under the OLD key.
	if _, _, err := NewStore(dir, "old-master-key").Resolve("proj1", "db_pw"); err == nil {
		t.Error("old key still decrypts a secret file after rotation")
	}
	// The in-progress marker is cleared once the verify pass is clean.
	meta, ok, err := readKDF(dir)
	if err != nil || !ok {
		t.Fatalf("read kdf: ok=%v err=%v", ok, err)
	}
	if meta.Rotating || len(meta.PrevSalt) != 0 {
		t.Errorf("rotation marker not cleared: %+v", meta)
	}
}

func TestRotate_WrongCurrentKeyChangesNothing(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "old-master-key")
	_ = s.Set("proj1", "db_pw", "s3cr3t")
	before, _ := os.ReadFile(s.path("proj1"))

	rep, err := s.Rotate("not-the-current-key", "new-master-key")
	if err == nil {
		t.Fatal("expected rotation to refuse a wrong current key")
	}
	if rep.Complete || len(rep.Unreadable) != 1 {
		t.Fatalf("expected the unreadable file to be named: %+v", rep)
	}
	after, _ := os.ReadFile(s.path("proj1"))
	if string(before) != string(after) {
		t.Error("a refused rotation must not rewrite anything")
	}
	// The store still reads under the real key.
	if v, ok, _ := NewStore(dir, "old-master-key").Resolve("proj1", "db_pw"); !ok || v != "s3cr3t" {
		t.Error("secret became unreadable after a refused rotation")
	}
}

// A rotation that dies after re-sealing only some files leaves a mixed store.
// It must stay READABLE (the running process keeps the previous key as a
// fallback) and RESUMABLE (re-running with the same old/new pair finishes it).
func TestRotate_InterruptedIsReadableAndResumable(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "old-master-key")
	_ = s.Set("done", "a", "1")
	_ = s.Set("pending", "b", "2")

	// Simulate the crash: mint the new generation, mark it in progress, and
	// re-seal only one of the two files — exactly the state a kill mid-loop
	// leaves behind.
	meta, _, _ := readKDF(dir)
	newSalt, _ := NewSalt()
	oldKey := DeriveKey("old-master-key", meta.Salt)
	newKey := DeriveKey("new-master-key", newSalt)
	if err := writeKDF(dir, kdfMeta{Version: 1, Salt: newSalt, PrevSalt: meta.Salt, Rotating: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "done.enc"))
	plain, err := decrypt(oldKey, raw)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := encrypt(newKey, plain)
	if err := os.WriteFile(filepath.Join(dir, "done.enc"), sealed, 0o600); err != nil {
		t.Fatal(err)
	}

	// Restarted with the OLD key: the un-rotated half still opens via the
	// previous-generation fallback rather than reading as lost.
	mixed := NewStore(dir, "old-master-key")
	if v, ok, err := mixed.Resolve("pending", "b"); err != nil || !ok || v != "2" {
		t.Fatalf("interrupted rotation lost the un-rotated half: %q %v %v", v, ok, err)
	}

	// Re-running the rotation finishes it instead of starting a third generation.
	rep, err := mixed.Rotate("old-master-key", "new-master-key")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !rep.Complete || rep.Rekeyed != 1 || rep.Verified != 2 {
		t.Fatalf("resume should re-key only the pending file: %+v", rep)
	}
	fresh := NewStore(dir, "new-master-key")
	for _, tc := range []struct{ proj, name, want string }{{"done", "a", "1"}, {"pending", "b", "2"}} {
		if v, ok, err := fresh.Resolve(tc.proj, tc.name); err != nil || !ok || v != tc.want {
			t.Errorf("%s/%s = %q (%v, %v)", tc.proj, tc.name, v, ok, err)
		}
	}
}

func TestRotate_RejectsBadInput(t *testing.T) {
	s := NewStore(t.TempDir(), "master")
	if _, err := s.Rotate("master", ""); err == nil {
		t.Error("empty new key should be rejected")
	}
	if _, err := s.Rotate("master", "master"); err == nil {
		t.Error("an unchanged key should be rejected")
	}
}

func TestLocalStatus_ReportsUnreadable(t *testing.T) {
	dir := t.TempDir()
	_ = NewStore(dir, "right-key").Set("p", "a", "b")

	files, values, unreadable := NewStore(dir, "right-key").LocalStatus()
	if files != 1 || values != 1 || len(unreadable) != 0 {
		t.Fatalf("healthy store: files=%d values=%d unreadable=%v", files, values, unreadable)
	}
	// A wrong key must be DISTINGUISHABLE from an empty store, which is what
	// lets a rotation verify itself and the panel say what cannot be read.
	files, values, unreadable = NewStore(dir, "wrong-key").LocalStatus()
	if files != 1 || values != 0 || len(unreadable) != 1 || unreadable[0] != "p" {
		t.Fatalf("wrong key: files=%d values=%d unreadable=%v", files, values, unreadable)
	}
}

func TestKDFFile_SurvivesAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	NewStore(dir, "master")
	data, err := os.ReadFile(filepath.Join(dir, kdfFile))
	if err != nil {
		t.Fatal(err)
	}
	var m kdfMeta
	if err := json.Unmarshal(data, &m); err != nil || len(m.Salt) == 0 {
		t.Fatalf("kdf.json: %v %+v", err, m)
	}
	if _, err := os.Stat(filepath.Join(dir, kdfFile+".tmp")); !os.IsNotExist(err) {
		t.Error("temp file left behind")
	}
}
