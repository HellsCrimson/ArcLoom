package model

import "testing"

// TestValidateFilePath is the guard on a value that becomes a filepath.Join
// argument in BOTH the project directory and the run workspace, so the
// traversal cases matter more than the happy ones.
func TestValidateFilePath(t *testing.T) {
	ok := map[string]string{
		"cloud-init.tftpl":            "cloud-init.tftpl",
		"templates/init.tftpl":        "templates/init.tftpl",
		"scripts/deep/nested/run.sh":  "scripts/deep/nested/run.sh",
		"modules/team/user-data.yaml": "modules/team/user-data.yaml",
		"policy.json":                 "policy.json",
		// Interior traversal that resolves back inside is cleaned, not rejected.
		"templates/../init.tftpl": "init.tftpl",
		"./init.tftpl":            "init.tftpl",
		// A dotfile is fine as the LAST segment (.gitkeep, .env.example).
		".env.example": ".env.example",
	}
	for in, want := range ok {
		got, err := ValidateFilePath(in)
		if err != nil {
			t.Errorf("ValidateFilePath(%q) = error %v, want %q", in, err, want)
			continue
		}
		if got != want {
			t.Errorf("ValidateFilePath(%q) = %q, want %q", in, got, want)
		}
	}

	bad := []string{
		"", "   ",
		"/etc/passwd",            // absolute
		"../outside.txt",         // escapes
		"../../etc/passwd",       // escapes further
		"templates/../../x.txt",  // escapes after cleaning
		"..",                     // bare
		".",                      // bare
		`templates\init.tftpl`,   // backslash separator survives path.Clean
		`..\..\windows`,          // backslash traversal
		".terraform/plugin",      // provider install dir
		".terraform",             //
		"main.tf",                // generated from the model
		"modules/team/main.tf",   //
		"extra.TF",               // case-insensitive
		"override.tf.json",       //
		"terraform.tfstate",      // would clobber real state after the sync wipe
		"terraform.tfstate.backup",
		"sub/terraform.tfstate", // reserved by base name too
		".arcloom.tfbackend",    // would silently repoint the backend
		"tfplan",                // owned by the approval/policy gates
		".terraform.lock.hcl",
		".hidden/x.txt", // dot-directory
	}
	for _, in := range bad {
		if got, err := ValidateFilePath(in); err == nil {
			t.Errorf("ValidateFilePath(%q) = %q, want an error", in, got)
		}
	}
}
