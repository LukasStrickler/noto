package bench

import (
	"os"
	"path/filepath"
)

// ModalPrereqs reports whether this machine can actually run a LIVE (billable)
// Modal benchmark — whether the plan's "Live Modal prerequisites" (§0.3) are in
// place: the dedicated bench interpreter (.venv-modal, which carries modal +
// torch + nemo + pyannote), Modal authentication (MODAL_TOKEN_ID/SECRET env or
// ~/.modal.toml), and the launcher script.
//
// A run without --integration-only shells out to that interpreter and
// authenticates against the user's Modal token, so without these a real run can
// only fail with a cryptic subprocess error. Probing up front lets the runner
// make the correct call: proceed when the credentials/keys are set, or surface a
// precise, actionable blocker when they are not.
type ModalPrereqs struct {
	HasVenv        bool
	HasCredentials bool
	HasScript      bool
}

// Ready reports whether a live Modal run can be attempted.
func (p ModalPrereqs) Ready() bool { return p.HasVenv && p.HasCredentials && p.HasScript }

// Blockers lists the missing prerequisites as actionable preflight blocker
// strings (empty when Ready). The `modal_prereq:` prefix is a stable token a
// caller can grep, while the remainder guides a human to the fix.
func (p ModalPrereqs) Blockers() []string {
	var out []string
	if !p.HasVenv {
		out = append(out, "modal_prereq: .venv-modal interpreter missing (run scripts/fetch-bench-deps.sh)")
	}
	if !p.HasCredentials {
		out = append(out, "modal_prereq: Modal credentials missing (run `modal token new` or set MODAL_TOKEN_ID/MODAL_TOKEN_SECRET)")
	}
	if !p.HasScript {
		out = append(out, "modal_prereq: scripts/modal_benchmark.py missing")
	}
	return out
}

// CheckModalPrereqs probes the live-Modal prerequisites under repoRoot.
func CheckModalPrereqs(repoRoot string) ModalPrereqs {
	_, hasVenv := venvPython(repoRoot)
	return ModalPrereqs{
		HasVenv:        hasVenv,
		HasCredentials: modalCredentialsPresent(),
		HasScript:      fileExists(filepath.Join(repoRoot, "scripts", "modal_benchmark.py")),
	}
}

// modalCredentialsPresent reports whether Modal auth is available the way the
// modal SDK resolves it: explicit token env vars, or a ~/.modal.toml (honoring
// the MODAL_CONFIG_PATH override).
func modalCredentialsPresent() bool {
	if os.Getenv("MODAL_TOKEN_ID") != "" && os.Getenv("MODAL_TOKEN_SECRET") != "" {
		return true
	}
	path := os.Getenv("MODAL_CONFIG_PATH")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		path = filepath.Join(home, ".modal.toml")
	}
	return fileExists(path)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
