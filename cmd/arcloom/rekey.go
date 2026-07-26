package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// rekeyCmd rotates ARCLOOM_SECRET_KEY offline: it re-seals the per-project
// encrypted secret files and the settings document's encrypted fields under a
// new master passphrase, then verifies both under the new key alone.
//
// This is the same operation the admin panel performs, run with the server
// STOPPED — which is the version with no window: the panel's rotation leaves
// the process using a key the environment does not yet hold until the operator
// updates it, whereas here the environment is updated before the next start.
// It is also the recovery path for a rotation interrupted halfway: re-running
// it with the same old/new pair finishes the job.
func rekeyCmd(args []string) int {
	fs := flag.NewFlagSet("rekey", flag.ContinueOnError)
	oldKey := fs.String("old", "", "current master key (defaults to ARCLOOM_SECRET_KEY)")
	newKey := fs.String("new", "", "new master key (required)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: arcloom rekey -new <new-key> [-old <current-key>]")
		fmt.Fprintln(os.Stderr, "\nRe-encrypts the local secret store and the encrypted settings fields")
		fmt.Fprintln(os.Stderr, "under a new master key. Run with the server stopped, and back up the")
		fmt.Fprintln(os.Stderr, "secrets directory and settings file first.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := config.Load()
	current := *oldKey
	if current == "" {
		current = cfg.SecretKey
	}
	if current == "" {
		fmt.Fprintln(os.Stderr, "rekey: no current key: pass -old or set ARCLOOM_SECRET_KEY")
		return 2
	}
	if strings.TrimSpace(*newKey) == "" {
		fmt.Fprintln(os.Stderr, "rekey: -new is required")
		return 2
	}
	if *newKey == current {
		fmt.Fprintln(os.Stderr, "rekey: the new key must differ from the current one")
		return 2
	}

	fmt.Printf("secrets dir:   %s\nsettings file: %s\n\n", cfg.SecretsDir, cfg.SettingsFile)

	failed := false
	sec := secrets.NewStore(cfg.SecretsDir, current)
	rep, err := sec.Rotate(current, *newKey)
	report("secret files", rep, err)
	failed = failed || err != nil

	set, serr := settings.NewStore(cfg.SettingsFile, current)
	if serr != nil {
		fmt.Fprintf(os.Stderr, "settings: %v\n", serr)
		failed = true
	} else {
		srep, err := set.Rotate(current, *newKey)
		report("settings fields", srep, err)
		failed = failed || err != nil
	}

	if failed {
		fmt.Fprintln(os.Stderr, "\nrekey INCOMPLETE — the old key is still needed for the items listed above.")
		fmt.Fprintln(os.Stderr, "Re-run this command with the same -old/-new to finish it, or restore your backup.")
		return 1
	}
	fmt.Printf("\nrekey complete. Set ARCLOOM_SECRET_KEY to the new key before starting the server.\n")
	return 0
}

func report(what string, rep secrets.RotateReport, err error) {
	fmt.Printf("%-16s %d found, %d re-keyed, %d verified", what, rep.Items, rep.Rekeyed, rep.Verified)
	if rep.Values > 0 {
		fmt.Printf(" (%d value(s))", rep.Values)
	}
	fmt.Println()
	if len(rep.Unreadable) > 0 {
		fmt.Printf("%-16s UNREADABLE: %s\n", "", strings.Join(rep.Unreadable, ", "))
	}
	if err != nil {
		fmt.Printf("%-16s error: %v\n", "", err)
	}
}
