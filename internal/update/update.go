// Package update checks the Go module proxy for a newer release and installs it through
// `go install`, caching the answer beside state.json so status and the TUI stay offline.
package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alesierraalta/tpp/internal/buildinfo"
	"github.com/alesierraalta/tpp/internal/state"
)

const (
	// DefaultBaseURL is the public Go module proxy the check queries.
	DefaultBaseURL = "https://proxy.golang.org"
	// The project was renamed to tsp: this last tpp release checks and installs the tsp module, so an
	// existing install reaches the new name through update.
	modulePath    = "github.com/alesierraalta/tsp"
	cacheFileName = "update-check.json"
	checkTimeout  = 5 * time.Second
)

// ErrCheck marks every failure to learn the latest version from the proxy, so the CLI can tell
// "the check failed" apart from any other error and still record it in the cache.
var ErrCheck = errors.New("update check failed")

// ErrGoMissing marks a machine with no go on PATH: the caller prints InstallCommand instead of
// running anything, and that print-and-exit is a success, not a failure.
var ErrGoMissing = errors.New("go not found on PATH")

// Relation is how the installed build stands against the latest tag the proxy reports.
type Relation int

const (
	// Unknown: a version on either side cannot be compared numerically.
	Unknown Relation = iota
	// Equal: the proxy has nothing newer (it also covers an installed build ahead of the tag).
	Equal
	// Behind: a newer tag exists and `go install` would move the binary forward.
	Behind
)

// Checker queries the proxy; the zero value is ready against DefaultBaseURL with a bounded
// client. BaseURL and HTTP are the seams tests point at httptest.
type Checker struct {
	BaseURL string       // empty means DefaultBaseURL
	HTTP    *http.Client // nil means a client bounded by checkTimeout
}

// Result is one check answered against the running build.
type Result struct {
	Latest   string
	Relation Relation
}

// Check asks the proxy for the module's latest version and compares it with buildinfo.Version.
// Proxy tags carry a v prefix; the installed version may not, so Compare normalizes both.
func (c Checker) Check(ctx context.Context) (Result, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: checkTimeout}
	}
	url := strings.TrimRight(base, "/") + "/" + modulePath + "/@latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, fmt.Errorf("%w: build request: %v", ErrCheck, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrCheck, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("%w: %s answered %s", ErrCheck, url, resp.Status)
	}
	var payload struct {
		Version string
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("%w: read %s: %v", ErrCheck, url, err)
	}
	if payload.Version == "" {
		return Result{}, fmt.Errorf("%w: %s answered with no version", ErrCheck, url)
	}
	return Result{Latest: payload.Version, Relation: Compare(buildinfo.Version, payload.Version)}, nil
}

// Compare orders two version strings component-wise after dropping a leading v. A version that
// yields no numeric component is Unknown rather than a wrong verdict.
func Compare(installed, latest string) Relation {
	left, leftOK := parseVersion(installed)
	right, rightOK := parseVersion(latest)
	if !leftOK || !rightOK {
		return Unknown
	}
	for i := 0; i < len(left) || i < len(right); i++ {
		l, r := 0, 0
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if r > l {
			return Behind
		}
		if l > r {
			return Equal
		}
	}
	return Equal
}

// Uncomparable says why Compare answered Unknown, naming the side that carries no release number: an
// untagged module reaches the proxy as a pseudo-version, and a local build may carry no numeric version at all.
func Uncomparable(installed, latest string) string {
	if _, ok := parseVersion(installed); !ok {
		return fmt.Sprintf("cannot compare: installed %s is not a release version (latest %s)", installed, latest)
	}
	return fmt.Sprintf("cannot compare: latest %s is not a release version (installed %s); releases are tagged vX.Y.Z", latest, installed)
}

func parseVersion(version string) ([]int, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if trimmed == "" {
		return nil, false
	}
	parts := strings.Split(trimmed, ".")
	nums := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	return nums, true
}

// Cache is the last check's outcome, stored under the state root so status and the TUI read it
// instead of the network. Error-only entries record a failed check; AvailableVersion is empty
// there, and status falls back to its unknown string.
type Cache struct {
	AvailableVersion string `json:"availableVersion,omitempty"`
	CheckedAt        string `json:"checkedAt"`
	Error            string `json:"error,omitempty"`
}

// LoadCache answers an empty cache when no check has ever been recorded.
func LoadCache() (Cache, error) {
	path, err := cachePath()
	if err != nil {
		return Cache{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Cache{}, nil
	}
	if err != nil {
		return Cache{}, fmt.Errorf("read update cache %s: %w", path, err)
	}
	var entry Cache
	if err := json.Unmarshal(data, &entry); err != nil {
		return Cache{}, fmt.Errorf("update cache %s is corrupt: %w", path, err)
	}
	return entry, nil
}

// SaveCache writes only when the bytes would differ, so re-saving the same entry leaves the file
// untouched. It reports whether it wrote.
func SaveCache(entry Cache) (bool, error) {
	path, err := cachePath()
	if err != nil {
		return false, err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return false, fmt.Errorf("marshal update cache: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create state directory %s: %w", dir, err)
	}
	existing, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(existing, data):
		return false, nil
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, fmt.Errorf("read update cache %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return false, fmt.Errorf("write update cache %s: %w", path, err)
	}
	return true, nil
}

// cachePath is the cache file beside state.json: same root, same TPP_HOME/TPP_HOME/XDG resolution.
func cachePath() (string, error) {
	statePath, err := state.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), cacheFileName), nil
}

// InstallCommand is the exact line the CLI prints when it cannot run the install itself.
func InstallCommand(latest string) string {
	return "go " + strings.Join(installArgs(latest), " ")
}

func installArgs(latest string) []string {
	return []string{"install", modulePath + "/cmd/tsp@" + latest}
}

// RunInstall looks `go` up and runs the install through the injected seams: production wires
// exec.LookPath and a streaming runner, tests wire fakes and never touch the network or the
// module cache. ErrGoMissing means print InstallCommand and exit successfully.
func RunInstall(latest string, lookPath func(string) (string, error), run func(name string, args ...string) error) error {
	if _, err := lookPath("go"); err != nil {
		return ErrGoMissing
	}
	return run("go", installArgs(latest)...)
}
