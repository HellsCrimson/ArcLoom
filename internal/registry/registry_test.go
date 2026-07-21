package registry

import (
	"os"
	"path/filepath"
	"testing"

	goversion "github.com/hashicorp/go-version"
)

func TestSplitSource(t *testing.T) {
	cases := []struct{ in, ns, name string }{
		{"random", "hashicorp", "random"},
		{"hashicorp/random", "hashicorp", "random"},
		{"registry.terraform.io/hashicorp/random", "hashicorp", "random"},
		{"integrations/github", "integrations", "github"},
	}
	for _, c := range cases {
		ns, name := SplitSource(c.in)
		if ns != c.ns || name != c.name {
			t.Errorf("SplitSource(%q) = %q/%q, want %q/%q", c.in, ns, name, c.ns, c.name)
		}
	}
}

func TestLatestStable(t *testing.T) {
	mk := func(vs ...string) []*goversion.Version {
		out := make([]*goversion.Version, 0, len(vs))
		for _, v := range vs {
			out = append(out, goversion.Must(goversion.NewVersion(v)))
		}
		return out
	}
	// A prerelease newer than every stable release must not win.
	got := LatestStable(mk("3.6.0", "3.6.2", "3.7.0-rc1", "3.5.1"))
	if got != "3.6.2" {
		t.Errorf("LatestStable = %q, want 3.6.2", got)
	}
	if got := LatestStable(mk("2.0.0-beta")); got != "" {
		t.Errorf("LatestStable(prerelease only) = %q, want empty", got)
	}
	if got := LatestStable(nil); got != "" {
		t.Errorf("LatestStable(nil) = %q, want empty", got)
	}
}

func TestInstalledVersionsAndFor(t *testing.T) {
	lock := `# This file is maintained automatically by "terraform init".
provider "registry.terraform.io/hashicorp/random" {
  version     = "3.6.0"
  constraints = "3.6.0"
  hashes = [
    "h1:abc",
  ]
}

provider "registry.terraform.io/hashicorp/null" {
  version = "3.2.1"
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, ".terraform.lock.hcl")
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	installed := InstalledVersions(path)
	if got := installed["registry.terraform.io/hashicorp/random"]; got != "3.6.0" {
		t.Errorf("random = %q, want 3.6.0", got)
	}
	if got := InstalledFor(installed, "hashicorp/random"); got != "3.6.0" {
		t.Errorf("InstalledFor(hashicorp/random) = %q, want 3.6.0", got)
	}
	if got := InstalledFor(installed, "null"); got != "3.2.1" {
		t.Errorf("InstalledFor(null) = %q, want 3.2.1", got)
	}
	if got := InstalledFor(installed, "hashicorp/aws"); got != "" {
		t.Errorf("InstalledFor(missing) = %q, want empty", got)
	}
}

func TestInstalledVersionsMissingFile(t *testing.T) {
	got := InstalledVersions(filepath.Join(t.TempDir(), "nope.hcl"))
	if len(got) != 0 {
		t.Errorf("missing lock file should yield empty map, got %v", got)
	}
}
