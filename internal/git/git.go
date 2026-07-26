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
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpauth "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/pmezard/go-difflib/difflib"
	cryptossh "golang.org/x/crypto/ssh"
)

const gitignore = `# ArcLoom — never commit state or secrets
*.tfstate
*.tfstate.*
*.tfvars
.terraform/
.terraform.lock.hcl
secrets.enc
.env
run/
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
	// Ahead/Behind count commits the local branch is ahead of / behind its
	// origin tracking ref (refs/remotes/origin/<branch>). Both 0 when there is
	// no tracking ref yet (e.g. never pushed/pulled).
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
}

// Branch is one local branch.
type Branch struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Short   string `json:"short"` // short hash of the branch tip
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
// For repos created by earlier ArcLoom versions it upgrades the .gitignore so
// the persistent run/ workspace (state, provider install) stays untracked.
func EnsureInit(dir string) error {
	if _, err := gogit.PlainOpen(dir); err == nil {
		return ensureRunIgnored(dir)
	} else if !errors.Is(err, gogit.ErrRepositoryNotExists) {
		return err
	}
	if _, err := gogit.PlainInit(dir, false); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644)
}

func ensureRunIgnored(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(gitignore), 0o644)
	} else if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "run/" {
			return nil
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return os.WriteFile(path, append(data, "run/\n"...), 0o644)
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
		st.Ahead, st.Behind = aheadBehind(repo, head)
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

// aheadBehind counts how far the local HEAD branch is ahead of / behind its
// origin tracking ref. Returns (0, 0) when no such tracking ref exists (never
// synced). Cheap for the tiny config-only repos ArcLoom manages.
func aheadBehind(repo *gogit.Repository, head *plumbing.Reference) (ahead, behind int) {
	trackName := plumbing.NewRemoteReferenceName("origin", head.Name().Short())
	track, err := repo.Reference(trackName, true)
	if err != nil {
		return 0, 0
	}
	local := reachable(repo, head.Hash())
	remote := reachable(repo, track.Hash())
	for h := range local {
		if !remote[h] {
			ahead++
		}
	}
	for h := range remote {
		if !local[h] {
			behind++
		}
	}
	return ahead, behind
}

// reachable returns the set of commit hashes reachable from a starting commit.
func reachable(repo *gogit.Repository, from plumbing.Hash) map[plumbing.Hash]bool {
	seen := map[plumbing.Hash]bool{}
	iter, err := repo.Log(&gogit.LogOptions{From: from})
	if err != nil {
		return seen
	}
	_ = iter.ForEach(func(c *object.Commit) error {
		seen[c.Hash] = true
		return nil
	})
	return seen
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

// CommitChanges stages changes and commits them, returning the new hash. When
// paths is empty every change is staged; otherwise only the listed paths are
// staged (per-file staging), leaving the rest as uncommitted working changes.
func CommitChanges(dir, message, name, email string, paths []string) (string, error) {
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
	if err := stage(wt, dir, paths); err != nil {
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

// stage adds the given paths to the index (or everything when paths is empty).
// A path whose file no longer exists on disk is staged as a deletion.
func stage(wt *gogit.Worktree, dir string, paths []string) error {
	if len(paths) == 0 {
		return wt.AddWithOptions(&gogit.AddOptions{All: true})
	}
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, p)); os.IsNotExist(err) {
			if _, err := wt.Remove(p); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			continue
		}
		if _, err := wt.Add(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

// ErrCommitPushed reports that the commit being undone is already on the
// origin tracking ref, so dropping it locally would rewrite published history.
// Callers may proceed by passing force.
var ErrCommitPushed = errors.New("the last commit is already pushed to origin")

// UndoLastCommit drops the newest commit while keeping every file on disk — the
// equivalent of `git reset HEAD~1`, so the work reappears as uncommitted changes
// the user can re-commit differently.
//
// ArcLoom otherwise only ever moves history forward (Restore commits a revert
// rather than rewriting), which is why this is the single rewriting operation
// and why it refuses a pushed commit unless force is set: undoing locally what
// origin already has diverges the branch, and merge here is fast-forward only.
//
// The ROOT commit is handled too: there is no parent to reset onto, so HEAD's
// branch ref is deleted and the index emptied, returning the repo to its
// pre-first-commit state (an unborn branch with everything untracked) instead of
// failing the way `git reset HEAD~1` does.
func UndoLastCommit(dir string, force bool) (Commit, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return Commit{}, err
	}
	head, err := repo.Head()
	if err != nil {
		return Commit{}, fmt.Errorf("no commits to undo")
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		return Commit{}, err
	}
	// A pushed commit is only detectable when a tracking ref exists; ahead == 0
	// with one present means origin already has this commit.
	if !force {
		if ahead, _ := aheadBehind(repo, head); ahead == 0 {
			if _, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", head.Name().Short()), true); err == nil {
				return Commit{}, ErrCommitPushed
			}
		}
	}
	undone := commitFrom(c)

	if c.NumParents() == 0 {
		// Root commit: drop the branch ref and empty the index. Files stay on
		// disk and read back as untracked, which is what the next commit stages.
		if err := repo.Storer.RemoveReference(head.Name()); err != nil {
			return Commit{}, fmt.Errorf("remove %s: %w", head.Name(), err)
		}
		if err := repo.Storer.SetIndex(&index.Index{Version: 2}); err != nil {
			return Commit{}, fmt.Errorf("clear index: %w", err)
		}
		return undone, nil
	}

	parent, err := c.Parent(0)
	if err != nil {
		return Commit{}, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return Commit{}, err
	}
	// MixedReset moves the branch and resets the index but leaves the working
	// tree alone — the undone content shows up as ordinary uncommitted changes.
	// A hard reset here would silently delete the user's work.
	if err := wt.Reset(&gogit.ResetOptions{Commit: parent.Hash, Mode: gogit.MixedReset}); err != nil {
		return Commit{}, err
	}
	return undone, nil
}

func commitFrom(c *object.Commit) Commit {
	return Commit{
		Hash:    c.Hash.String(),
		Short:   c.Hash.String()[:7],
		Message: c.Message,
		Author:  c.Author.Name,
		Email:   c.Author.Email,
		Date:    c.Author.When,
	}
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
		out = append(out, commitFrom(c))
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

// Branches lists local branches, newest listing sorted by name, with the
// current branch flagged.
func Branches(dir string) ([]Branch, error) {
	repo, err := gogit.PlainOpen(dir)
	if errors.Is(err, gogit.ErrRepositoryNotExists) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	current := ""
	if head, err := repo.Head(); err == nil {
		current = head.Name().Short()
	}
	iter, err := repo.Branches()
	if err != nil {
		return nil, err
	}
	var out []Branch
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()
		out = append(out, Branch{Name: name, Current: name == current, Short: ref.Hash().String()[:7]})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CreateBranch creates a new branch from the current HEAD and switches to it.
// Uncommitted changes are carried over (Keep); a checkout conflict surfaces as
// an error so nothing is silently discarded.
func CreateBranch(dir, name string) error {
	if err := validateBranchName(name); err != nil {
		return err
	}
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	if _, err := repo.Head(); err != nil {
		return fmt.Errorf("commit at least once before creating a branch")
	}
	ref := plumbing.NewBranchReferenceName(name)
	if _, err := repo.Reference(ref, false); err == nil {
		return fmt.Errorf("branch %q already exists", name)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Checkout(&gogit.CheckoutOptions{Branch: ref, Create: true, Keep: true})
}

// SwitchBranch checks out an existing branch, keeping uncommitted changes. A
// conflict (the target branch would overwrite local edits) surfaces as an error.
func SwitchBranch(dir, name string) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	ref := plumbing.NewBranchReferenceName(name)
	if _, err := repo.Reference(ref, false); err != nil {
		return fmt.Errorf("branch %q not found", name)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	// No Keep/Force: go-git updates tracked files to the target branch when the
	// worktree is clean and errors (rather than discarding) when local edits
	// would be overwritten — so a switch can't silently lose uncommitted work.
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: ref}); err != nil {
		return fmt.Errorf("switch to %q: %w (commit or discard local changes first)", name, err)
	}
	return nil
}

// DeleteBranch removes a branch (its ref and any config entry). The current
// branch cannot be deleted.
func DeleteBranch(dir, name string) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	if head, err := repo.Head(); err == nil && head.Name().Short() == name {
		return fmt.Errorf("cannot delete the current branch %q", name)
	}
	ref := plumbing.NewBranchReferenceName(name)
	if _, err := repo.Reference(ref, false); err != nil {
		return fmt.Errorf("branch %q not found", name)
	}
	if err := repo.Storer.RemoveReference(ref); err != nil {
		return err
	}
	_ = repo.DeleteBranch(name) // best-effort config cleanup
	return nil
}

// MergeBranch fast-forward merges the named branch into the current branch and
// syncs the working tree to the result. go-git supports only fast-forward
// merges; a diverged history is reported clearly. A dirty worktree is rejected
// up front so a hard reset can't discard uncommitted work.
func MergeBranch(dir, name string) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	st, err := wt.Status()
	if err != nil {
		return err
	}
	if !st.IsClean() {
		return fmt.Errorf("commit or discard local changes before merging")
	}
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(name), true)
	if err != nil {
		return fmt.Errorf("branch %q not found", name)
	}
	err = repo.Merge(*ref, gogit.MergeOptions{Strategy: gogit.FastForwardMerge})
	if errors.Is(err, gogit.ErrFastForwardMergeNotPossible) {
		return fmt.Errorf("cannot fast-forward merge %q: the branches have diverged (only fast-forward merges are supported)", name)
	}
	if err != nil {
		return err
	}
	// Merge only moves the branch pointer; bring the working tree to the tip.
	return wt.Reset(&gogit.ResetOptions{Mode: gogit.HardReset, Commit: ref.Hash()})
}

// validateBranchName rejects names Git (and go-git) would refuse, keeping the
// error at the API boundary rather than deep in a checkout.
func validateBranchName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("branch name is required")
	}
	if strings.ContainsAny(name, " ~^:?*[\\") || strings.Contains(name, "..") ||
		strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("invalid branch name %q", name)
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

// Auth carries the credentials for a remote operation. The transport is chosen
// from the remote URL: an SSH URL (git@host:… or ssh://…) uses SSHKey, an HTTPS
// URL uses Username/Token. Fields left empty select an unauthenticated attempt.
type Auth struct {
	Username   string // HTTPS username (optional; defaults to "arcloom")
	Token      string // HTTPS personal access token / password
	SSHKey     string // SSH private key (PEM)
	Passphrase string // SSH key passphrase (optional)
}

// Push pushes the current branch to origin.
func Push(dir string, a Auth) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	am, err := authFor(repo, a)
	if err != nil {
		return err
	}
	err = repo.Push(&gogit.PushOptions{RemoteName: "origin", Auth: am})
	if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

// Pull pulls from origin into the current branch's worktree.
func Pull(dir string, a Auth) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	am, err := authFor(repo, a)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	err = wt.Pull(&gogit.PullOptions{RemoteName: "origin", Auth: am})
	if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

// authFor builds the transport auth for the origin remote, picking SSH vs HTTPS
// from its URL. Returns nil (anonymous) when no matching credential is supplied.
func authFor(repo *gogit.Repository, a Auth) (transport.AuthMethod, error) {
	url := originURL(repo)
	if isSSHURL(url) {
		return sshAuth(url, a)
	}
	if a.Token == "" {
		return nil, nil
	}
	username := a.Username
	if username == "" {
		username = "arcloom" // GitHub ignores the username when a PAT is the password
	}
	return &httpauth.BasicAuth{Username: username, Password: a.Token}, nil
}

// sshAuth builds public-key auth from a stored private key. Host-key
// verification is intentionally skipped: ArcLoom is a single-node homelab tool
// with no interactive known_hosts prompt, and the remote is user-configured.
func sshAuth(url string, a Auth) (transport.AuthMethod, error) {
	if a.SSHKey == "" {
		return nil, fmt.Errorf("this remote uses SSH — add an SSH private key credential")
	}
	user := sshUser(url)
	keys, err := gitssh.NewPublicKeys(user, []byte(a.SSHKey), a.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("load SSH key: %w", err)
	}
	keys.HostKeyCallback = cryptossh.InsecureIgnoreHostKey()
	return keys, nil
}

func originURL(repo *gogit.Repository) string {
	rem, err := repo.Remote("origin")
	if err != nil {
		return ""
	}
	if urls := rem.Config().URLs; len(urls) > 0 {
		return urls[0]
	}
	return ""
}

// isSSHURL reports whether a remote URL uses the SSH transport (scp-style
// git@host:path or an explicit ssh:// scheme).
func isSSHURL(url string) bool {
	if strings.HasPrefix(url, "ssh://") {
		return true
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return false
	}
	// scp-style "user@host:path": an '@' and a ':' before any '/'.
	at := strings.IndexByte(url, '@')
	colon := strings.IndexByte(url, ':')
	slash := strings.IndexByte(url, '/')
	return at > 0 && colon > at && (slash == -1 || colon < slash)
}

// sshUser extracts the login from an SSH remote URL, defaulting to "git".
func sshUser(url string) string {
	rest := strings.TrimPrefix(url, "ssh://")
	if at := strings.IndexByte(rest, '@'); at > 0 {
		return rest[:at]
	}
	return "git"
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
