package envvar

import "testing"

// The refusal list is a privilege boundary, not tidiness: these names become
// environment variables of a process the SERVER spawns, and the permission to
// edit them is `resources`, which is not admin. PATH alone is arbitrary code
// execution as the ArcLoom process.
func TestValidate_RefusesEscalation(t *testing.T) {
	for _, name := range []string{
		"PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "HOME", "SHELL",
		"TF_CLI_ARGS", "TF_CLI_ARGS_plan", "TF_CLI_CONFIG_FILE",
		"TF_DATA_DIR", "TF_PLUGIN_CACHE_DIR", "TF_WORKSPACE",
		// Not dangerous — terraform-exec overwrites these in the child env, so
		// accepting them would save a row that silently does nothing.
		"TF_LOG", "TF_LOG_CORE", "TF_LOG_PROVIDER", "TF_LOG_PATH",
		"TF_INPUT", "TF_IN_AUTOMATION", "TF_REATTACH_PROVIDERS",
		"TF_APPEND_USER_AGENT", "TF_DISABLE_PLUGIN_TLS", "TF_SKIP_PROVIDER_VERIFY",
		"ARCLOOM_SECRET_KEY", "ARCLOOM_DATA", "ARCLOOM_ANYTHING",
		"TF_VAR_password", // the variable/secret path owns this one
		// Case must not be a way around any of it.
		"Path", "tf_cli_args", "arcloom_secret_key",
	} {
		if err := Validate(name); err == nil {
			t.Errorf("Validate(%q) allowed it", name)
		}
	}
}

func TestValidate_AllowsOrdinaryNames(t *testing.T) {
	for _, name := range []string{
		"OPENNEBULA_USERNAME", "OPENNEBULA_PASSWORD", "OPENNEBULA_ENDPOINT",
		"VAULT_TOKEN", "VAULT_ADDR", "AWS_PROFILE", "AWS_REGION",
		"http_proxy", "_LEADING_UNDERSCORE", "TF_REGISTRY_DISCOVERY_RETRY",
	} {
		if err := Validate(name); err != nil {
			t.Errorf("Validate(%q) = %v, want allowed", name, err)
		}
	}
}

// A name that is not a valid environment-variable name at all: one carrying `=`
// or a space would be a different variable than it appears to be.
func TestValidate_RejectsMalformedNames(t *testing.T) {
	for _, name := range []string{"", "  ", "1LEADING_DIGIT", "HAS SPACE", "HAS=EQUALS", "HAS-DASH", "a.b"} {
		if err := Validate(name); err == nil {
			t.Errorf("Validate(%q) allowed it", name)
		}
	}
}

func TestNormalize(t *testing.T) {
	got, err := Normalize([]Var{
		{Name: "  VAULT_ADDR  ", Value: "https://vault.lan"},
		{}, // a blank form row is skipped, not an error
		{Name: "VAULT_TOKEN", Value: "ignored", SecretRef: "vault_token"},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Name != "VAULT_ADDR" {
		t.Errorf("name not trimmed: %q", got[0].Name)
	}
	// A secret-backed row must not also carry a literal, or which one wins would
	// depend on the reader.
	if got[1].Value != "" || got[1].SecretRef != "vault_token" {
		t.Errorf("secret row kept a literal: %+v", got[1])
	}
}

func TestNormalize_RejectsDuplicates(t *testing.T) {
	if _, err := Normalize([]Var{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}}); err == nil {
		t.Error("two rows for one name have no defined winner and must be refused")
	}
}

// The env layers over the project by name: the project row holds the shape and
// an environment overrides only what differs.
func TestMerge(t *testing.T) {
	got := Merge(
		[]Var{{Name: "ENDPOINT", Value: "https://dev"}, {Name: "USER", Value: "dev"}},
		[]Var{{Name: "ENDPOINT", Value: "https://prod"}, {Name: "EXTRA", Value: "x"}},
	)
	want := map[string]string{"ENDPOINT": "https://prod", "USER": "dev", "EXTRA": "x"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for _, v := range got {
		if want[v.Name] != v.Value {
			t.Errorf("%s = %q, want %q", v.Name, v.Value, want[v.Name])
		}
	}
}

// TestValidateRefusesExecutionAndTrustVars covers the names added after the
// security audit. The cross-user case is the one that matters: a project editor
// holds `resources` and cannot run anything themselves, but these fire the next
// time somebody with `deploy` runs a plan.
func TestValidateRefusesExecutionAndTrustVars(t *testing.T) {
	for _, name := range []string{
		// git executes these, and `init` shells out to git for a git:: module.
		"GIT_SSH_COMMAND", "GIT_SSH", "GIT_PROXY_COMMAND", "GIT_EXTERNAL_DIFF",
		"GIT_CONFIG_GLOBAL", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
		// the shell sources these before a provisioner runs
		"BASH_ENV", "ENV", "LD_AUDIT",
		// certificate trust — what keeps an allowed proxy from being a MITM
		"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS",
		"GIT_SSL_NO_VERIFY",
		// engine configuration ArcLoom owns
		"TOFU_CLI_CONFIG_FILE", "TERRAFORM_CONFIG", "XDG_CONFIG_HOME", "GODEBUG",
		// case-insensitivity: the map is keyed upper-case, the check folds
		"git_ssh_command", "ssl_cert_file",
	} {
		if err := Validate(name); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}

	// Proxy variables stay ALLOWED on purpose: routing engine traffic through a
	// corporate proxy is legitimate, and with the trust store locked down above a
	// proxy that cannot forge a certificate sees only metadata.
	for _, name := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy",
		// ...as do ordinary provider credentials, which are the whole point.
		"AWS_PROFILE", "OPENNEBULA_PASSWORD", "VAULT_TOKEN", "KUBECONFIG",
	} {
		if err := Validate(name); err != nil {
			t.Errorf("%s must stay allowed: %v", name, err)
		}
	}
}
