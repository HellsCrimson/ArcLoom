package envvar

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Environment variables passed to the engine process for a project's runs.
//
// Why this exists: a provider's own environment variables are often the
// documented way to give it credentials (OPENNEBULA_PASSWORD, VAULT_TOKEN,
// AWS_PROFILE), and ArcLoom could not set one. The only injection it had was
// TF_VAR_<name>, which feeds `variable` blocks and nothing else, so the only
// workaround was to set the variable on the ArcLoom PROCESS — instance-wide,
// shared by every project, and a container restart to change.
//
// Values are either a literal or a reference to a stored secret. A literal is
// kept in settings.yaml in the clear, exactly like Environment.Vars, so anything
// sensitive belongs in the secret store and is referenced by name — that is the
// whole reason SecretRef exists.

// Var is one environment variable for a project's engine runs.
type Var struct {
	Name string `yaml:"name" json:"name"`
	// Value is a literal, stored in the clear. Ignored when SecretRef is set.
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
	// SecretRef names a secret in the project's store, resolved at run time
	// through the same path a plan's TF_VAR_* secrets take — so the value is
	// never persisted here and a broken store fails the run loudly.
	SecretRef string `yaml:"secret_ref,omitempty" json:"secretRef,omitempty"`
}

// nameRe is the portable environment-variable name: a C identifier.
// Deliberately strict — a name with `=` or a NUL would be a different variable
// than it appears to be, and lowercase names are allowed because some tools use
// them (`http_proxy`).
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// blocked are names a project may not set, because setting them is not
// "configure my provider" but "change what the server runs".
//
// The variables are applied to a process the SERVER spawns, and the permission
// needed to edit them is `resources`, not admin — so this keeps a project editor
// from changing what the server runs.
//
// It is NOT what stops `resources` reaching code execution: that permission can
// already write a `provisioner "local-exec"` block, which no entry here affects
// (internal/runner/sandbox.go is the containment for it). Treating this list as
// that boundary is a mistake worth not repeating. What it does buy is the
// cross-user case — a project editor planting something that fires the next time
// a `deploy` user runs a plan, which is a privilege they do not otherwise have.
var blocked = map[string]string{
	// Arbitrary code execution: the engine binary, or anything it loads.
	"PATH":                  "it selects which binaries the engine runs",
	"LD_PRELOAD":            "it loads arbitrary code into the engine process",
	"LD_LIBRARY_PATH":       "it loads arbitrary code into the engine process",
	"DYLD_INSERT_LIBRARIES": "it loads arbitrary code into the engine process",
	"DYLD_LIBRARY_PATH":     "it loads arbitrary code into the engine process",
	"HOME":                  "it relocates the CLI's credentials and plugin cache",
	"SHELL":                 "it selects which shell provisioners run",
	// Arbitrary CLI flags — including -chdir and alternate provider mirrors.
	"TF_CLI_ARGS":          "it injects arbitrary CLI arguments into every command",
	"TF_CLI_CONFIG_FILE":   "it repoints provider installation",
	"TOFU_CLI_CONFIG_FILE": "it repoints provider installation (the OpenTofu spelling)",
	"TERRAFORM_CONFIG":     "it repoints provider installation (the legacy spelling)",
	"XDG_CONFIG_HOME":      "the engine reads its CLI configuration from under it",
	"TFE_TOKEN":            "it is a credential ArcLoom does not manage",

	// Command execution through git. `terraform init` shells out to the real git
	// binary for a `git::` module source, and git executes these — so a project
	// editor could otherwise run commands as the server the next time somebody
	// with `deploy` ran a plan. That is a cross-user escalation, which the
	// sandbox bounds but does not prevent.
	"GIT_SSH":            "git executes it",
	"GIT_SSH_COMMAND":    "git executes it",
	"GIT_PROXY_COMMAND":  "git executes it",
	"GIT_EXTERNAL_DIFF":  "git executes it",
	"GIT_EDITOR":         "git executes it",
	"GIT_CONFIG_GLOBAL":  "it can set core.sshCommand, which git executes",
	"GIT_CONFIG_SYSTEM":  "it can set core.sshCommand, which git executes",
	"GIT_CONFIG_COUNT":   "it injects arbitrary git config, including commands git executes",
	"GIT_ALLOW_PROTOCOL": "it re-enables transports git disables for safety",

	// Shell startup files, for the same reason: a provisioner runs through a
	// shell, and these make it source a script first.
	"BASH_ENV":     "the shell executes it before running a provisioner",
	"ENV":          "the shell executes it before running a provisioner",
	"SHELLOPTS":    "it changes how the shell running a provisioner behaves",
	"LD_AUDIT":     "it loads arbitrary code into the engine process",
	"GODEBUG":      "it changes the engine's TLS and runtime behaviour",
	"GOFLAGS":      "it alters how Go-based tooling builds and runs",
	"GOPROXY":      "it repoints where Go-based tooling fetches code",
	"GOPRIVATE":    "it repoints where Go-based tooling fetches code",
	"GONOSUMDB":    "it disables checksum verification for Go-based tooling",
	"GONOSUMCHECK": "it disables checksum verification for Go-based tooling",
	"GOSUMDB":      "it disables checksum verification for Go-based tooling",

	// TLS trust. These decide which certificate authorities the engine and its
	// providers believe, so setting one turns any proxy into a machine-in-the
	// -middle that can read the cloud credentials flowing through it.
	//
	// Proxy variables themselves (HTTP_PROXY, HTTPS_PROXY, NO_PROXY) are
	// deliberately ALLOWED: routing engine traffic through a corporate proxy is a
	// legitimate, common need, and a proxy that cannot forge a certificate sees
	// only metadata. Blocking the trust store is what keeps that true, so these
	// two decisions belong together.
	"SSL_CERT_FILE":       "it decides which certificate authorities the engine trusts",
	"SSL_CERT_DIR":        "it decides which certificate authorities the engine trusts",
	"CURL_CA_BUNDLE":      "it decides which certificate authorities the engine trusts",
	"REQUESTS_CA_BUNDLE":  "it decides which certificate authorities the engine trusts",
	"NODE_EXTRA_CA_CERTS": "it decides which certificate authorities the engine trusts",
	"GIT_SSL_CAINFO":      "it decides which certificate authorities git trusts",
	"GIT_SSL_NO_VERIFY":   "it disables certificate verification for git",
	// Workspace ownership: these decide where state and plugins live, which the
	// runner needs to control for the persistent per-project workspace to work.
	"TF_DATA_DIR":         "ArcLoom owns the run workspace layout",
	"TF_PLUGIN_CACHE_DIR": "ArcLoom sets the shared plugin cache",
	"ARCLOOM_SECRET_KEY":  "it is the server's own key",
	"ARCLOOM_VAULT_TOKEN": "it is the server's own credential",

	// Refused for a DIFFERENT reason: terraform-exec (the library that spawns the
	// CLI) keeps its own prohibitedEnvVars list and overwrites every one of these
	// in the child environment — TF_LOG* unconditionally, "so logging can't
	// pollute our stderr output". Accepting them would be a lie: the row would
	// save, the panel would look right, and the value would never reach the
	// engine. Refusing says so instead of leaving the user to find out.
	"TF_LOG":                  "the engine wrapper clears it so logs cannot corrupt the parsed output",
	"TF_LOG_CORE":             "the engine wrapper clears it so logs cannot corrupt the parsed output",
	"TF_LOG_PROVIDER":         "the engine wrapper clears it so logs cannot corrupt the parsed output",
	"TF_LOG_PATH":             "the engine wrapper clears it, and it would write logs outside the run workspace",
	"TF_INPUT":                "the engine wrapper sets it — a run must never wait for interactive input",
	"TF_IN_AUTOMATION":        "the engine wrapper sets it for every run",
	"TF_WORKSPACE":            "the engine wrapper sets it; ArcLoom selects the workspace per environment",
	"TF_REATTACH_PROVIDERS":   "the engine wrapper controls provider reattachment",
	"TF_APPEND_USER_AGENT":    "the engine wrapper sets the user agent",
	"TF_DISABLE_PLUGIN_TLS":   "the engine wrapper controls plugin transport security",
	"TF_SKIP_PROVIDER_VERIFY": "the engine wrapper controls provider verification",
	"CHECKPOINT_DISABLE":      "the engine wrapper propagates it from the server's own environment",
}

// Validate reports whether a name may be set by a project.
//
// TF_CLI_ARGS_* is refused by prefix: the suffixed forms
// (TF_CLI_ARGS_plan, ...) do the same thing as the bare one. Every ARCLOOM_*
// name is refused outright — the server's own configuration is not a project's
// business, and an allowlist of two would rot. TF_VAR_* is refused because the
// secret/variable path owns it: setting one here would collide silently with a
// resolved secret and there would be no way to see which won. GIT_CONFIG_KEY_*
// / GIT_CONFIG_VALUE_* go the same way as GIT_CONFIG_COUNT: together they inject
// arbitrary git configuration, including commands git will execute.
//
// **What this list is and is not.** Editing these needs `resources`, not admin,
// and they become environment variables of a process the SERVER spawns — so the
// list keeps a project editor from changing what the server runs. It does NOT
// make `resources` safe: that permission already implies code execution via a
// `provisioner "local-exec"` block, which no denylist here can affect. The
// containment for that is internal/runner/sandbox.go. What this list still buys
// is the cross-user case: a project editor who cannot run anything themselves
// planting something that fires when a `deploy` user next runs a plan.
func Validate(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return fmt.Errorf("name is required")
	}
	if !nameRe.MatchString(n) {
		return fmt.Errorf("invalid name %q (letters, digits and _, not starting with a digit)", n)
	}
	upper := strings.ToUpper(n)
	if strings.HasPrefix(upper, "TF_VAR_") {
		return fmt.Errorf("%s: use a variable or a secret — TF_VAR_* is set from those", n)
	}
	if strings.HasPrefix(upper, "TF_CLI_ARGS") {
		return fmt.Errorf("%s: it injects arbitrary CLI arguments into every command", n)
	}
	if strings.HasPrefix(upper, "ARCLOOM_") {
		return fmt.Errorf("%s: ARCLOOM_* is the server's own configuration", n)
	}
	if strings.HasPrefix(upper, "GIT_CONFIG_KEY_") || strings.HasPrefix(upper, "GIT_CONFIG_VALUE_") {
		return fmt.Errorf("%s: it injects arbitrary git config, including commands git executes", n)
	}
	if why, blocked := blocked[upper]; blocked {
		return fmt.Errorf("%s: %s", n, why)
	}
	return nil
}

// BlockedNames lists the refused names, sorted, so a UI can show them before a
// save fails. The prefix rules (TF_VAR_*, TF_CLI_ARGS*, ARCLOOM_*) are not in
// it — they are described in the UI copy instead.
func BlockedNames() []string {
	out := make([]string, 0, len(blocked))
	for name := range blocked {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Normalize trims, drops blank rows, rejects invalid or blocked names
// and rejects duplicates (two rows for one name have no defined winner).
func Normalize(in []Var) ([]Var, error) {
	var out []Var
	seen := map[string]bool{}
	for _, v := range in {
		name := strings.TrimSpace(v.Name)
		ref := strings.TrimSpace(v.SecretRef)
		if name == "" && ref == "" && strings.TrimSpace(v.Value) == "" {
			continue // a blank row is an unfilled form line, not an error
		}
		if err := Validate(name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate environment variable %q", name)
		}
		seen[name] = true
		e := Var{Name: name, SecretRef: ref}
		if ref == "" {
			e.Value = v.Value
		}
		out = append(out, e)
	}
	return out, nil
}

// Merge layers per-environment rows over the project's, by name. The env
// wins, which is the point: the project row holds the shape (and usually the
// dev value) and an environment overrides the ones that differ.
func Merge(project, env []Var) []Var {
	at := map[string]int{}
	out := make([]Var, 0, len(project)+len(env))
	for _, v := range project {
		at[v.Name] = len(out)
		out = append(out, v)
	}
	for _, v := range env {
		if i, ok := at[v.Name]; ok {
			out[i] = v
			continue
		}
		at[v.Name] = len(out)
		out = append(out, v)
	}
	return out
}
