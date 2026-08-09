package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Containment for the engine child process.
//
// Why this exists at all: a user holding `resources` can write a
// `provisioner "local-exec"` block, and a user holding `deploy` runs it — so
// between them they execute arbitrary commands as the ArcLoom process. That is
// inherent to wrapping the Terraform CLI (providers are downloaded binaries the
// engine executes too), and no amount of input validation changes it. What can
// change is what those commands can reach.
//
// Two layers, in order of how much they can be relied on:
//
//  1. **The environment scrub, always.** The engine is spawned by tfexec, which
//     inherits os.Environ() — and it has to, because tfexec's SetEnv refuses
//     TF_VAR_* (see prohibitedEnvVarPrefixes in terraform-exec), which is how
//     every secret reaches a run. So the child sees the server's whole
//     environment, including ARCLOOM_SECRET_KEY: the master key that decrypts
//     every project's secrets, the SSO client secret and the stored git tokens.
//     One `local-exec` echoing $ARCLOOM_SECRET_KEY took the lot. It also saw
//     ARCLOOM_SECRET_<name> for every OTHER project, which is a cross-project
//     leak on its own. Scrubbing has to happen in the CHILD (unsetting in the
//     server would race every other request), which is what the shim below is.
//
//  2. **A namespace sandbox, when the host can do it.** bubblewrap gives the
//     child a filesystem view with ArcLoom's own data — secrets, users,
//     sessions, API keys, OAuth grants, other projects — simply absent, rather
//     than merely unreadable-by-convention.
//
// The sandbox is probed, never assumed: unprivileged user namespaces are
// disabled on plenty of hosts and inside many container runtimes, and a sandbox
// that fails closed by default would break working installs on upgrade. Mode
// "require" is there for operators who would rather fail than run uncontained.
type Sandbox struct {
	// Mode is "auto" (sandbox if bwrap is present AND works), "off" (scrub only)
	// or "require" (refuse to run engine ops at all if bwrap does not work).
	Mode string
	// HideDirs are absolute directories the child must not see: ArcLoom's own
	// state. Each is masked with an empty tmpfs, so it reads as present but
	// empty rather than as forbidden.
	//
	// Directories and files are separate lists rather than one list plus a stat,
	// because the sandbox is configured at start-up — before the stores have
	// created their directories on a fresh install — so "does this path exist
	// yet, and is it a dir?" is not answerable at the point the shim is written.
	HideDirs []string
	// HideFiles are absolute files to mask with /dev/null.
	HideFiles []string
	// Writable are absolute paths the child needs write access to beyond its own
	// working directory — the shared provider plugin cache and the scratch dir
	// for ephemeral workspaces. Created if absent, since a bind mount needs its
	// source to exist.
	Writable []string
}

// Sandbox modes.
const (
	SandboxAuto    = "auto"
	SandboxOff     = "off"
	SandboxRequire = "require"
)

// bwrapBin is the sandbox helper. Bubblewrap rather than a container runtime
// because it needs no daemon and no socket: handing ArcLoom a Docker socket to
// isolate itself would grant it root on the host, which is a strictly worse
// trade than the problem it solves.
const bwrapBin = "bwrap"

// sandboxProbeTimeout bounds the start-up check. A hung probe must not hold up
// the server.
const sandboxProbeTimeout = 5 * time.Second

// UseSandbox configures containment for this runner's engine child processes
// and writes the shim they are launched through. It replaces the exec path with
// the shim; ExecPath keeps reporting the real engine binary, which is what the
// instance panel and the version check mean by it.
//
// Errors only in "require" mode: everywhere else a sandbox that cannot start is
// reported and degraded, exactly like tflint/conftest being absent.
func (t *TfExec) UseSandbox(s Sandbox) error {
	mode := s.Mode
	if mode == "" {
		mode = SandboxAuto
	}
	sandboxed := false
	switch mode {
	case SandboxOff:
	case SandboxAuto, SandboxRequire:
		if err := probeSandbox(); err != nil {
			if mode == SandboxRequire {
				return fmt.Errorf("ARCLOOM_SANDBOX=require but %s cannot start: %w", bwrapBin, err)
			}
			slog.Warn("engine sandbox unavailable; running with the environment scrub only",
				"engine", t.engine, "err", err,
				"hint", "install bubblewrap and allow unprivileged user namespaces for filesystem isolation")
		} else {
			sandboxed = true
		}
	default:
		return fmt.Errorf("unknown sandbox mode %q (use auto, off or require)", mode)
	}

	// A bind mount needs its source to exist, and these are ArcLoom's own working
	// directories — created lazily on first use, which is after this runs on a
	// fresh install.
	for _, p := range s.Writable {
		if p != "" {
			_ = os.MkdirAll(p, 0o755)
		}
	}
	dir, err := os.MkdirTemp("", "arcloom-shim-")
	if err != nil {
		return fmt.Errorf("create shim dir: %w", err)
	}
	shim := filepath.Join(dir, "engine")
	if err := os.WriteFile(shim, []byte(shimScript(t.realExecPath(), s, sandboxed)), 0o700); err != nil {
		return fmt.Errorf("write engine shim: %w", err)
	}
	t.shimDir = dir
	t.execPath = shim
	t.sandboxed = sandboxed
	slog.Info("engine containment ready", "engine", t.engine,
		"sandbox", sandboxed, "scrubbed", len(scrubbedNames()))
	return nil
}

// probeSandbox checks that bwrap is not just installed but actually usable.
// Unprivileged user namespaces are commonly disabled (a sysctl, a container
// runtime's seccomp profile, some hardened kernels), and bwrap then fails at
// the first run rather than at install time — so it is exercised here, once, on
// a trivial command, instead of surfacing as a mysterious failed apply.
func probeSandbox() error {
	path, err := exec.LookPath(bwrapBin)
	if err != nil {
		return fmt.Errorf("%s not found on PATH", bwrapBin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--ro-bind", "/", "/", "--dev", "/dev", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s probe failed: %s", bwrapBin, msg)
	}
	return nil
}

// scrubbedNames lists the environment variables the child must not inherit:
// every ARCLOOM_* the server was started with. That is the master key
// (ARCLOOM_SECRET_KEY), the external secret store's token, the seeded admin
// password, the SSO client secret, and every ARCLOOM_SECRET_<name> env secret —
// which belong to whichever project references them, not to the project whose
// run happens to be executing.
//
// Read from the server's own environment at start-up, so the list is exactly
// what is really there rather than a hand-maintained guess that would rot the
// next time a variable is added.
func scrubbedNames() []string {
	var out []string
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if name := kv[:eq]; strings.HasPrefix(name, "ARCLOOM_") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// shimScript renders the wrapper the engine is launched through.
//
// A generated script rather than doing this in Go because tfexec owns the
// exec.Cmd: it decides the argv and the environment, and exposes neither. The
// one thing it does let us choose is the path of the binary to run — so the
// containment goes there, transparently, and every operation inherits it
// (plan, apply, state surgery, schema fetches, the version probe) with no call
// site needing to remember.
//
// $PWD is the run workspace: tfexec sets the command's directory, so the shim
// does not need to be told which project it is running for — which is what lets
// one shim, written once at start-up, serve every project.
func shimScript(engine string, s Sandbox, sandboxed bool) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Generated by ArcLoom. Rewritten on every start; edits are lost.\n")
	b.WriteString("# Containment for the engine child process — see internal/runner/sandbox.go.\n")
	if names := scrubbedNames(); len(names) > 0 {
		b.WriteString("\n# ArcLoom's own configuration is not the engine's business, and\n")
		b.WriteString("# ARCLOOM_SECRET_KEY in particular opens every stored secret.\n")
		b.WriteString("unset " + strings.Join(names, " ") + "\n")
	}
	b.WriteString("\n")
	if !sandboxed {
		b.WriteString("exec " + shQuote(engine) + " \"$@\"\n")
		return b.String()
	}
	args := []string{
		// Everything readable, then the parts that must not be, masked back out.
		// An allowlist would be tighter but breaks real installs: providers read
		// ~/.aws/credentials, /etc/resolv.conf, CA bundles and their own plugin
		// directories, and guessing that set wrong fails a deploy in a way the
		// user cannot diagnose.
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}
	// A masked directory becomes an empty tmpfs and a masked file /dev/null:
	// both mean "this is not here" rather than "you may not read this", so a
	// provisioner cannot even enumerate what exists.
	for _, p := range s.HideDirs {
		if p != "" {
			args = append(args, "--tmpfs", p)
		}
	}
	for _, p := range s.HideFiles {
		if p != "" {
			// -try: the settings file does not exist until something is
			// configured, and a missing source aborts bwrap outright.
			args = append(args, "--ro-bind-try", "/dev/null", p)
		}
	}
	// The working directory is restored AFTER the masks: a project's run
	// workspace lives inside the data dir that was just hidden, so order is what
	// makes "hide the data dir, keep this one run" expressible at all.
	args = append(args, "--bind", "$PWD", "$PWD")
	for _, p := range s.Writable {
		if p != "" {
			args = append(args, "--bind-try", p, p)
		}
	}
	args = append(args,
		"--chdir", "$PWD",
		// A provisioner that forks a background process must not outlive the run.
		"--unshare-pid",
		"--die-with-parent",
		"--new-session", // no terminal to inject keystrokes into
	)

	b.WriteString("# Filesystem isolation: ArcLoom's secrets, users, sessions, API keys,\n")
	b.WriteString("# OAuth grants and other projects are absent from this view entirely.\n")
	b.WriteString("exec " + bwrapBin)
	for _, a := range args {
		// $PWD must reach the shell unquoted-but-safe: it is the one value the
		// shim resolves at run time rather than at write time.
		if a == "$PWD" {
			b.WriteString(" \"$PWD\"")
			continue
		}
		b.WriteString(" " + shQuote(a))
	}
	b.WriteString(" -- " + shQuote(engine) + " \"$@\"\n")
	return b.String()
}

// shQuote renders s as a single-quoted POSIX shell word. Every path here comes
// from ArcLoom's own configuration rather than from a request, but the shim is
// an executable file and a path containing a space would otherwise split into
// two arguments and mount the wrong thing.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
