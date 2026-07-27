package model

import (
	"errors"
	"path"
	"strings"
)

// Companion-file limits. Files live inside arcloom.yaml and inside every undo
// snapshot, so they are bounded well below the API body cap: these are meant
// for templates and scripts, not for shipping assets.
const (
	MaxFileSize      = 256 << 10 // 256 KiB per file
	MaxFilesPerScope = 100
)

var (
	ErrFilePathEmpty     = errors.New("file path is required")
	ErrFilePathAbsolute  = errors.New("file path must be relative (no leading '/')")
	ErrFilePathTraversal = errors.New("file path must not contain '..'")
	ErrFilePathHCL       = errors.New("HCL files are generated from the model — import .tf/.tf.json instead of adding it as a file")
	ErrFilePathReserved  = errors.New("file path is reserved by the workspace")
	ErrFilePathSegment   = errors.New("file path segment is not allowed")
	ErrFileTooLarge      = errors.New("file is too large")
)

// reservedFileNames are workspace-owned paths a companion file must never
// shadow. The files map is written into the run directory AFTER the sync wipe,
// so a file called "terraform.tfstate" would overwrite real state, and one
// called ".arcloom.tfbackend" would silently repoint the backend.
var reservedFileNames = map[string]bool{
	"terraform.tfstate":              true,
	"terraform.tfstate.backup":       true,
	"errored.tfstate":                true,
	".terraform.lock.hcl":            true,
	".terraform.tfstate.lock.info":   true,
	".arcloom.tfbackend":             true,
	"tfplan":                         true,
	"tfplan.drift":                   true,
	"tfplan.policy":                  true,
	".terraformrc":                   true,
	"terraform.rc":                   true,
}

// ValidateFilePath checks a companion file's path before it is stored, because
// the key flows straight into filepath.Join for both the project directory
// (store.Disk.persist) and the run workspace (runner.newWorkspace). It returns
// the cleaned, slash-separated path to store.
//
// Rejected: absolute paths, any ".." segment, "." segments, backslashes (a
// Windows-style separator would not be split by path.Clean and would become a
// literal filename), a leading-dot directory, .tf/.tf.json (HCL is generated
// from the model — a hand-added .tf would be invisible to the UI and could
// redeclare a resource), and anything shadowing a workspace-owned file.
func ValidateFilePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", ErrFilePathEmpty
	}
	if strings.ContainsRune(p, '\\') {
		return "", ErrFilePathSegment
	}
	if strings.HasPrefix(p, "/") {
		return "", ErrFilePathAbsolute
	}
	clean := path.Clean(p)
	// path.Clean resolves interior "..", so re-check the RESULT: a path that
	// still escapes (or is bare "." / "..") never had a valid target.
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrFilePathTraversal
	}
	if strings.HasPrefix(clean, "/") {
		return "", ErrFilePathAbsolute
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", ErrFilePathTraversal
		}
		// A dot-directory would hide the file from the user and, for
		// ".terraform", collide with the provider install.
		if strings.HasPrefix(seg, ".") && seg != path.Base(clean) {
			return "", ErrFilePathSegment
		}
	}
	lower := strings.ToLower(clean)
	if strings.HasSuffix(lower, ".tf") || strings.HasSuffix(lower, ".tf.json") {
		return "", ErrFilePathHCL
	}
	if reservedFileNames[lower] || reservedFileNames[strings.ToLower(path.Base(clean))] {
		return "", ErrFilePathReserved
	}
	if strings.HasPrefix(lower, ".terraform/") || lower == ".terraform" {
		return "", ErrFilePathReserved
	}
	return clean, nil
}
