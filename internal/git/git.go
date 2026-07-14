// Package git provides per-project version control over the generated
// configuration (arcloom.yaml + generated/*.tf) using an embedded Git
// implementation (go-git). State files and secrets are excluded via .gitignore
// and by living outside the project folder.
package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpauth "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/pmezard/go-difflib/difflib"
)

const gitignore = `# ArcLoom — never commit state or secrets
*.tfstate
*.tfstate.*
*.tfvars
.terraform/
.terraform.lock.hcl
secrets.enc
.env
`

// FileStatus is a changed file in the working tree.
type FileStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "modified" | "added" | "deleted" | "untracked"
}

// Status summarizes the repository state.
type Status struct {
	Initialized bool         `json:"initialized"`
	Branch      string       `json:"branch"`
	Clean       bool         `json:"clean"`
	Files       []FileStatus `json:"files"`
	HasCommits  bool         `json:"hasCommits"`
	HasRemote   bool         `json:"hasRemote"`
	RemoteURL   string       `json:"remoteUrl,omitempty"`
}

// Commit is one entry in the history.
type Commit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	Date    time.Time `json:"date"`
}

// EnsureInit initializes the repo (with a .gitignore) if it does not exist.
func EnsureInit(dir string) error {
	if _, err := gogit.PlainOpen(dir); err == nil {
		return nil
	} else if !errors.Is(err, gogit.ErrRepositoryNotExists) {
		return err
	}
	if _, err := gogit.PlainInit(dir, false); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644)
}

// GetStatus reports the working-tree status.
func GetStatus(dir string) (Status, error) {
	repo, err := gogit.PlainOpen(dir)
	if errors.Is(err, gogit.ErrRepositoryNotExists) {
		return Status{Initialized: false}, nil
	}
	if err != nil {
		return Status{}, err
	}
	st := Status{Initialized: true}

	wt, err := repo.Worktree()
	if err != nil {
		return Status{}, err
	}
	status, err := wt.Status()
	if err != nil {
		return Status{}, err
	}
	st.Clean = status.IsClean()
	for path, s := range status {
		st.Files = append(st.Files, FileStatus{Path: path, Status: statusWord(s)})
	}
	sort.Slice(st.Files, func(i, j int) bool { return st.Files[i].Path < st.Files[j].Path })

	if head, err := repo.Head(); err == nil {
		st.HasCommits = true
		st.Branch = head.Name().Short()
	}
	if remotes, _ := repo.Remotes(); len(remotes) > 0 {
		urls := remotes[0].Config().URLs
		if len(urls) > 0 {
			st.HasRemote = true
			st.RemoteURL = urls[0]
		}
	}
	return st, nil
}

func statusWord(s *gogit.FileStatus) string {
	code := s.Worktree
	if code == gogit.Unmodified {
		code = s.Staging
	}
	switch code {
	case gogit.Untracked:
		return "untracked"
	case gogit.Added:
		return "added"
	case gogit.Deleted:
		return "deleted"
	default:
		return "modified"
	}
}

// CommitChanges stages all changes and commits them, returning the new hash.
func CommitChanges(dir, message, name, email string) (string, error) {
	if message == "" {
		return "", fmt.Errorf("commit message is required")
	}
	if err := EnsureInit(dir); err != nil {
		return "", err
	}
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return "", err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
		return "", fmt.Errorf("stage: %w", err)
	}
	if name == "" {
		name = "ArcLoom"
	}
	if email == "" {
		email = "arcloom@localhost"
	}
	h, err := wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{Name: name, Email: email, When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	return h.String(), nil
}

// Log returns up to limit commits, newest first.
func Log(dir string, limit int) ([]Commit, error) {
	repo, err := gogit.PlainOpen(dir)
	if errors.Is(err, gogit.ErrRepositoryNotExists) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	head, err := repo.Head()
	if err != nil {
		return nil, nil // no commits yet
	}
	iter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}
	var out []Commit
	_ = iter.ForEach(func(c *object.Commit) error {
		if limit > 0 && len(out) >= limit {
			return storer("stop")
		}
		out = append(out, Commit{
			Hash:    c.Hash.String(),
			Short:   c.Hash.String()[:7],
			Message: c.Message,
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			Date:    c.Author.When,
		})
		return nil
	})
	return out, nil
}

// storer is a sentinel error to stop iteration.
type storer string

func (s storer) Error() string { return string(s) }

// WorkingDiff returns a unified diff of uncommitted changes vs HEAD.
func WorkingDiff(dir string) (string, error) {
	repo, err := gogit.PlainOpen(dir)
	if errors.Is(err, gogit.ErrRepositoryNotExists) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	status, err := wt.Status()
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(status))
	for p := range status {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var diff string
	for _, p := range paths {
		oldContent, _ := fileAtHead(repo, p)
		newContent := ""
		if b, err := os.ReadFile(filepath.Join(dir, p)); err == nil {
			newContent = string(b)
		}
		if oldContent == newContent {
			continue
		}
		diff += unified(p, oldContent, newContent)
	}
	return diff, nil
}

// CommitDiff returns a unified diff for one commit vs its parent.
func CommitDiff(dir, hash string) (string, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return "", err
	}
	c, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return "", err
	}
	if c.NumParents() == 0 {
		// Root commit: diff against an empty tree (all files added).
		tree, err := c.Tree()
		if err != nil {
			return "", err
		}
		var diff string
		_ = tree.Files().ForEach(func(f *object.File) error {
			content, _ := f.Contents()
			diff += unified(f.Name, "", content)
			return nil
		})
		return diff, nil
	}
	parent, err := c.Parent(0)
	if err != nil {
		return "", err
	}
	patch, err := parent.Patch(c)
	if err != nil {
		return "", err
	}
	return patch.String(), nil
}

// Restore rewrites the working tree to match a commit (non-destructive: the
// branch pointer is unchanged, so the result appears as uncommitted changes the
// user can review and commit as a revert).
func Restore(dir, hash string) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	c, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return err
	}
	tree, err := c.Tree()
	if err != nil {
		return err
	}
	target := map[string]bool{}
	if err := tree.Files().ForEach(func(f *object.File) error {
		content, err := f.Contents()
		if err != nil {
			return err
		}
		target[f.Name] = true
		full := filepath.Join(dir, f.Name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.WriteFile(full, []byte(content), 0o644)
	}); err != nil {
		return err
	}
	// Remove currently-tracked files that are absent from the target commit.
	if head, err := repo.Head(); err == nil {
		if hc, err := repo.CommitObject(head.Hash()); err == nil {
			if ht, err := hc.Tree(); err == nil {
				_ = ht.Files().ForEach(func(f *object.File) error {
					if !target[f.Name] {
						os.Remove(filepath.Join(dir, f.Name))
					}
					return nil
				})
			}
		}
	}
	return nil
}

// SetRemote sets the "origin" remote URL.
func SetRemote(dir, url string) error {
	if err := EnsureInit(dir); err != nil {
		return err
	}
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	_ = repo.DeleteRemote("origin")
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}})
	return err
}

// Push pushes to origin over HTTPS with a token.
func Push(dir, username, token string) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	err = repo.Push(&gogit.PushOptions{RemoteName: "origin", Auth: auth(username, token)})
	if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

// Pull pulls from origin over HTTPS with a token.
func Pull(dir, username, token string) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	err = wt.Pull(&gogit.PullOptions{RemoteName: "origin", Auth: auth(username, token)})
	if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

func auth(username, token string) transport.AuthMethod {
	if token == "" {
		return nil
	}
	if username == "" {
		username = "arcloom" // GitHub ignores the username when a PAT is the password
	}
	return &httpauth.BasicAuth{Username: username, Password: token}
}

func fileAtHead(repo *gogit.Repository, path string) (string, bool) {
	head, err := repo.Head()
	if err != nil {
		return "", false
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", false
	}
	f, err := c.File(path)
	if err != nil {
		return "", false
	}
	content, err := f.Contents()
	if err != nil {
		return "", false
	}
	return content, true
}

func unified(path, oldContent, newContent string) string {
	s, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldContent),
		B:        difflib.SplitLines(newContent),
		FromFile: "a/" + path,
		ToFile:   "b/" + path,
		Context:  3,
	})
	return s
}
