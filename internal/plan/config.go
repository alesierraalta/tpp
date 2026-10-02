package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConfigName is the canonical repository-local declaration read from the worktree root.
const ConfigName = ".tsp.json"

// LegacyConfigName is an older declaration name that remains readable for compatibility.
const LegacyConfigName = ".rdd-plus.json"

// Reader reads a file's text; nil means os.ReadFile.
type Reader func(path string) (string, error)

type declaration struct {
	planPath string
	run      string
}

var runSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)

// readDeclaration reads the repository declaration once and resolves both keys. A declaration that
// cannot be read, does not parse, or carries an unusable value is an error: a typo must never read as
// "nothing declared".
func readDeclaration(root string, read Reader) (declaration, error) {
	if read == nil {
		read = func(path string) (string, error) {
			body, err := os.ReadFile(path)
			return string(body), err
		}
	}
	name, body, err := readDeclarationFile(root, read)
	if err != nil || name == "" {
		return declaration{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		return declaration{}, fmt.Errorf("%s: %w", name, err)
	}
	if fields == nil {
		return declaration{}, fmt.Errorf("%s: declaration must be a JSON object", name)
	}
	// The key check must be a map check: Go's decoder matches struct field names case-insensitively,
	// so {"planpath": ...} would decode into the field and DisallowUnknownFields would not refuse it.
	// Checking the exact key is what makes a typo fail closed.
	for key := range fields {
		if key != "planPath" && key != "run" {
			return declaration{}, fmt.Errorf("%s: unknown field %q", name, key)
		}
	}

	var d declaration
	if declared, present := fields["planPath"]; present {
		if err := json.Unmarshal(declared, &d.planPath); err != nil {
			return declaration{}, fmt.Errorf("%s: planPath must be a string: %w", name, err)
		}
		if strings.TrimSpace(d.planPath) == "" {
			return declaration{}, fmt.Errorf("%s: planPath is empty", name)
		}
		if err := ValidatePlanPath(name, d.planPath); err != nil {
			return declaration{}, err
		}
	}
	if declared, present := fields["run"]; present {
		if err := json.Unmarshal(declared, &d.run); err != nil {
			return declaration{}, fmt.Errorf("%s: run must be a string: %w", name, err)
		}
		if err := ValidateRun(name, d.run); err != nil {
			return declaration{}, err
		}
	}
	return d, nil
}

// readDeclarationFile answers the name and body of the declaration the root carries: ConfigName, then
// the legacy names, else no name at all. Multiple present files fail closed, because silently choosing one
// would audit a plan the operator did not mean.
func readDeclarationFile(root string, read Reader) (string, string, error) {
	var found []string
	var body string
	for _, name := range []string{ConfigName, ".tpp.json", LegacyConfigName} {
		text, err := read(filepath.Join(root, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", name, err)
		}
		if len(found) == 0 {
			body = text
		}
		found = append(found, name)
	}
	switch len(found) {
	case 0:
		return "", "", nil
	case 1:
		return found[0], body, nil
	default:
		return "", "", fmt.Errorf("multiple declarations are present at the worktree root: %s", strings.Join(found, ", "))
	}
}

// DeclaredPath returns the repository-relative plan the root declares, or "" when the root declares
// nothing.
func DeclaredPath(root string, read Reader) (string, error) {
	d, err := readDeclaration(root, read)
	return d.planPath, err
}

// DeclaredRun returns the active run the root declares, or "" when no run key is present.
func DeclaredRun(root string, read Reader) (string, error) {
	d, err := readDeclaration(root, read)
	return d.run, err
}

// Resolve returns the effective plan path and declared run for a worktree in one declaration read.
func Resolve(root string, read Reader) (string, string, error) {
	d, err := readDeclaration(root, read)
	if err != nil {
		return "", "", err
	}
	if d.planPath == "" {
		d.planPath = DefaultPath
	}
	return d.planPath, d.run, nil
}

// ResolvePath is the effective plan path for a worktree: the declaration when there is one, else
// DefaultPath.
func ResolvePath(root string, read Reader) (string, error) {
	path, _, err := Resolve(root, read)
	return path, err
}

// ResolveRun is the effective declared run for a worktree, or "" when no run key is present.
func ResolveRun(root string, read Reader) (string, error) {
	_, run, err := Resolve(root, read)
	return run, err
}

// ValidateRun refuses an empty, malformed, or reserved run slug.
func ValidateRun(source, s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s must name a run", source)
	}
	if !runSlugRe.MatchString(s) {
		return fmt.Errorf("%s must be a valid run slug [a-z0-9][a-z0-9-]{1,63}: %q", source, s)
	}
	if s == "all" || s == "none" {
		return fmt.Errorf("%s cannot use reserved run %q", source, s)
	}
	return nil
}

// ResolveFromRoot turns a --path value into the file to read: an absolute value is taken as given, a
// relative one is resolved against root under the same containment rule the declaration obeys.
func ResolveFromRoot(root, source, p string) (string, error) {
	if filepath.IsAbs(p) {
		return p, nil
	}
	if err := ValidatePlanPath(source, p); err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.Clean(p)), nil
}

// ValidatePlanPath rejects a declaration path that is not repository-relative or that escapes the
// worktree. source names where the value came from (the config file, or the flag) for the message.
func ValidatePlanPath(source, p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("%s must name a plan path", source)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s must be repository-relative: %q is absolute", source, p)
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s escapes the worktree: %q", source, p)
	}
	return nil
}
