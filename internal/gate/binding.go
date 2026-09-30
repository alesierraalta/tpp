package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alesierraalta/tpp/internal/plan"
)

// BindingEnv carries the Stop gate's explicit per-session plan binding: validated JSON with the
// exact session_id, repository root, planPath, and optional run the audit may use. The gate never
// falls back to the worktree-wide declaration. The environment is one of two sources — the stored
// file under BindingsDir, written by `tpp bind`, is the other — and when both name this session
// they must agree or the stop audits nothing.
const BindingEnv = "TPP_GATE_PLAN_BINDING"

// SkippedUnbound is the telemetry reason for a stop whose sources carried no exact binding:
// missing, malformed, mismatched, and conflicting bindings are one fact to the operator — the
// gate audited nothing.
const SkippedUnbound = "session_plan_unbound"

// Binding is one session's audit binding, validated on parse and matched exactly before use.
type Binding struct {
	Session  string `json:"session_id"`
	Root     string `json:"root"`
	PlanPath string `json:"planPath"`
	Run      string `json:"run"`
}

// sessionBinding answers the binding for this stop from two sources: the environment's JSON and
// this session's stored file under dir. The file drives only when the environment is absent; when
// both are present they must agree — a disagreement fails closed with no fallback to either
// source. Anything absent, invalid, or belonging to another session or root answers false, and
// the caller audits nothing.
func sessionBinding(sessionID, root, dir string) (Binding, bool) {
	envRaw := strings.TrimSpace(os.Getenv(BindingEnv))
	file, hasFile, fileOK := fileBinding(dir, sessionID, root)
	switch {
	case envRaw != "" && hasFile:
		// Each source that validates matches this exact session and root, so plan and run are the
		// whole comparison: any difference is a conflict, and a conflict never falls back.
		env, envOK := matchBinding(envRaw, sessionID, root)
		if !envOK || !fileOK || env.PlanPath != file.PlanPath || env.Run != file.Run {
			return Binding{}, false
		}
		return env, true
	case envRaw != "":
		return matchBinding(envRaw, sessionID, root)
	case hasFile && fileOK:
		return file, true
	default:
		return Binding{}, false
	}
}

// matchBinding parses one binding source and matches it exactly against the session and the
// canonical repository root. Anything absent, invalid, or foreign answers false.
func matchBinding(raw, sessionID, root string) (Binding, bool) {
	b, err := parseBinding(raw)
	if err != nil || sessionID == "" || b.Session != sessionID {
		return Binding{}, false
	}
	boundRoot, err := canonicalRoot(b.Root)
	if err != nil {
		return Binding{}, false
	}
	thisRoot, err := canonicalRoot(root)
	if err != nil || boundRoot != thisRoot {
		return Binding{}, false
	}
	return b, true
}

// BindingsDir is where `tpp bind` stores bindings, derived from the same log path Stop writes so
// the directory holding the log and the directory holding its bindings can never disagree. An
// empty log path has no directory, and the file source simply does not exist.
func BindingsDir(logPath string) string {
	if logPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(logPath), "bindings")
}

// bindingPath names the file for one (canonical root, exact session) pair: a hash of the pair, so
// no filename in the telemetry directory ever carries a raw session ID.
func bindingPath(dir, root, sessionID string) string {
	sum := sha256.Sum256([]byte(root + "\x00" + sessionID))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
}

// fileBinding reads this session's stored binding from dir. present says a file sits at this
// session's key; ok says its content is a valid binding whose exact session and canonical root
// are the ones asked — the content check the hashed filename alone cannot make. Malformed bytes,
// a foreign session or root, and an unreadable file are present-but-not-ok: never a binding.
func fileBinding(dir, sessionID, root string) (b Binding, present, ok bool) {
	if dir == "" || sessionID == "" {
		return Binding{}, false, false
	}
	croot, err := canonicalRoot(root)
	if err != nil {
		return Binding{}, false, false
	}
	raw, err := os.ReadFile(bindingPath(dir, croot, sessionID))
	if err != nil {
		return Binding{}, !os.IsNotExist(err), false
	}
	parsed, err := parseBinding(string(raw))
	if err != nil {
		return Binding{}, true, false
	}
	boundRoot, err := canonicalRoot(parsed.Root)
	if parsed.Session != sessionID || err != nil || boundRoot != croot {
		return Binding{}, true, false
	}
	return parsed, true, true
}

// SetBinding validates b with the same rules an env binding obeys and stores it under its hashed
// key: a 0700 directory (tightened even when it already existed looser) and a 0600 file renamed
// into place, so a reader never sees a partial binding and a failed write leaves the previous one
// intact.
func SetBinding(dir string, b Binding) error {
	if dir == "" {
		return fmt.Errorf("binding: no binding directory derived from the gate log")
	}
	root, err := canonicalRoot(b.Root)
	if err != nil {
		return err
	}
	b.Root = root
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if _, err := parseBinding(string(raw)); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll only applies 0700 when it creates the directory; a pre-existing looser one must be
	// tightened here, before any write, so a binding is never stored in a directory others can read.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bind-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), bindingPath(dir, root, b.Session)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// UnsetBinding removes only the file this (root, session) key names — never another session's —
// and an absent file is already the goal.
func UnsetBinding(dir, sessionID, root string) error {
	if dir == "" {
		return fmt.Errorf("binding: no binding directory derived from the gate log")
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("binding: a session is required")
	}
	croot, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	if err := os.Remove(bindingPath(dir, croot, sessionID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// parseBinding fails closed: an object only, exact field names only, a nonempty session, a
// repository-relative plan path, and a valid run slug whenever the run key is present. A typo must
// never read as a weaker binding.
func parseBinding(raw string) (Binding, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return Binding{}, fmt.Errorf("%s: %w", BindingEnv, err)
	}
	if fields == nil {
		return Binding{}, fmt.Errorf("%s: binding must be a JSON object", BindingEnv)
	}
	for key := range fields {
		switch key {
		case "session_id", "root", "planPath", "run":
		default:
			return Binding{}, fmt.Errorf("%s: unknown field %q", BindingEnv, key)
		}
	}
	var b Binding
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return Binding{}, fmt.Errorf("%s: %w", BindingEnv, err)
	}
	if b.Session == "" {
		return Binding{}, fmt.Errorf("%s: session_id must be a nonempty session", BindingEnv)
	}
	if err := plan.ValidatePlanPath(BindingEnv, b.PlanPath); err != nil {
		return Binding{}, err
	}
	if _, present := fields["run"]; present {
		if err := plan.ValidateRun(BindingEnv, b.Run); err != nil {
			return Binding{}, err
		}
	}
	return b, nil
}

// canonicalRoot is the form two roots are compared in: absolute, and symlink-resolved when the
// path exists on disk. A path that cannot be made absolute is an error, so a relative or
// unresolvable root can never compare equal to the repository — fail closed, never bind.
func canonicalRoot(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s: root must be absolute: %q", BindingEnv, path)
	}
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved, nil
	}
	return cleaned, nil
}
