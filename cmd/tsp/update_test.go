package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alesierraalta/tsp/internal/buildinfo"
	"github.com/alesierraalta/tsp/internal/update"
)

// statusAvailable decodes the one field this suite cares about from `status --json`.
func statusAvailable(t *testing.T, out string) string {
	t.Helper()
	var report statusReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &report); err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	return report.AvailableVersion
}

// status stays offline: without a check it reports the unknown string, and after a cached check
// it carries the cached version — never a network call of its own.
func TestStatusAvailableVersionComesFromTheUpdateCache(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()

	out, code := runCLIWithHomeEnv(t, home, bin, "status", "--json")
	if code != 0 {
		t.Fatalf("status without a cache = %d\n%s", code, out)
	}
	if got := statusAvailable(t, out); got != unknownAvailableVersion {
		t.Fatalf("availableVersion without a check = %q, want %q", got, unknownAvailableVersion)
	}

	t.Setenv("TPP_HOME", home)
	if _, err := update.SaveCache(update.Cache{AvailableVersion: "v9.9.9", CheckedAt: "2026-09-22T12:00:00Z"}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	out, code = runCLIWithHomeEnv(t, home, bin, "status", "--json")
	if code != 0 {
		t.Fatalf("status with a cache = %d\n%s", code, out)
	}
	if got := statusAvailable(t, out); got != "v9.9.9" {
		t.Fatalf("availableVersion with a cached check = %q, want %q", got, "v9.9.9")
	}
}

// fakeProxy serves one @latest answer so the CLI can be driven end to end without the network.
func fakeProxy(t *testing.T, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"Version":%q}`, version)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// With go absent the check still succeeds, the cache is refreshed, and the exact install line is
// printed — print-and-exit is the success path, not a failure.
func TestUpdateWithoutGoPrintsTheInstallCommand(t *testing.T) {
	bin := buildCLI(t)
	srv := fakeProxy(t, "v99.0.0")
	home := t.TempDir()
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + home,
		"TPP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + t.TempDir(), // an empty directory: there is no go to find
	}, "update")
	if code != 0 {
		t.Fatalf("update without go = %d, want 0\n%s", code, out)
	}
	want := "go install github.com/alesierraalta/tsp/cmd/tsp@v99.0.0"
	if !strings.Contains(out, want) {
		t.Fatalf("output missing the exact install command %q:\n%s", want, out)
	}
	dir, _ := filepath.EvalSymlinks(filepath.Dir(bin))
	if hint := "GOBIN='" + dir + "' go install"; !strings.Contains(out, hint) {
		t.Fatalf("the hint must name the running binary's directory, shell-quoted (%q):\n%s", hint, out)
	}
	t.Setenv("TPP_HOME", home)
	cache, err := update.LoadCache()
	if err != nil {
		t.Fatalf("load cache after update: %v", err)
	}
	if cache.AvailableVersion != "v99.0.0" {
		t.Fatalf("cache = %+v, want the checked version recorded", cache)
	}
}

// A fake go on PATH receives exactly the install argv, and the success hint points at a new
// shell: the running process is still the old binary.
func TestUpdateWithAFakeGoRunsTheInstallArgv(t *testing.T) {
	bin := buildCLI(t)
	srv := fakeProxy(t, "v99.0.0")
	home := t.TempDir()
	fakeBin := t.TempDir()
	argsFile := filepath.Join(fakeBin, "go-args")
	gobinFile := filepath.Join(fakeBin, "go-gobin")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nprintf '%s' \"$GOBIN\" > '" + gobinFile + "'\nprintf '#!/bin/sh\\nexit 0\\n' > \"$GOBIN/tsp.next\"\n/bin/chmod +x \"$GOBIN/tsp.next\"\n/bin/mv \"$GOBIN/tsp.next\" \"$GOBIN/tsp\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + home,
		"TPP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + fakeBin,
	}, "update")
	if code != 0 {
		t.Fatalf("update with a fake go = %d, want 0\n%s", code, out)
	}
	argv, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("fake go was never run: %v", err)
	}
	// The project was renamed: the bridge release updates into the tsp module and binary.
	want := "install\ngithub.com/alesierraalta/tsp/cmd/tsp@v99.0.0\n"
	if string(argv) != want {
		t.Fatalf("install argv = %q, want %q", argv, want)
	}
	if !strings.Contains(out, "tsp version") || !strings.Contains(out, "tsp sync") {
		t.Fatalf("success must point at the renamed binary: confirm with tsp version and run tsp sync:\n%s", out)
	}
	// The release must replace the binary that is running, not land in a GOBIN that PATH or the Stop hook
	// never reach (issue #144).
	gobin, err := os.ReadFile(gobinFile)
	if err != nil {
		t.Fatalf("fake go left no GOBIN record: %v", err)
	}
	wantDir, _ := filepath.EvalSymlinks(filepath.Dir(bin))
	if string(gobin) != wantDir {
		t.Fatalf("go install ran with GOBIN=%q, want the running binary's directory %q", gobin, wantDir)
	}
	if r, err := filepath.EvalSymlinks(bin); err != nil || r != filepath.Join(wantDir, "tsp") {
		t.Fatalf("running tpp resolves to %q (err %v), want it symlinked to the fresh install %s (issue #144)", r, err, filepath.Join(wantDir, "tsp"))
	}
}

func TestCanonicalTspUpdateKeepsTheInstalledExecutable(t *testing.T) {
	bin := buildCLI(t, "tsp")
	srv := fakeProxy(t, "v99.0.0")
	home := t.TempDir()
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte("#!/bin/sh\nprintf '#!/bin/sh\\nexit 0\\n' > \"$GOBIN/tsp.next\"\n/bin/chmod +x \"$GOBIN/tsp.next\"\n/bin/mv \"$GOBIN/tsp.next\" \"$GOBIN/tsp\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runCLIEnv(t, bin, []string{
		"TSP_HOME=" + home,
		"TSP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + fakeBin,
	}, "update")
	if code != 0 {
		t.Fatalf("canonical tsp update = %d, want 0\n%s", code, out)
	}
	info, err := os.Lstat(bin)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("canonical executable is not a regular file: info=%v err=%v", info, err)
	}
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil || resolved != bin {
		t.Fatalf("canonical executable resolves to %q, err %v; want %q", resolved, err, bin)
	}
	entries, err := os.ReadDir(filepath.Dir(bin))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tsp-update-link-") || strings.HasPrefix(entry.Name(), ".tpp-update-link-") {
			t.Fatalf("pending bridge link remains: %s", entry.Name())
		}
	}
	if !strings.Contains(out, "installed tsp v99.0.0") || strings.Contains(out, "tpp is now tsp") {
		t.Fatalf("canonical success message is inaccurate:\n%s", out)
	}
}

// A proxy that fails exits 1 with a clear error, records the failure in the cache, and leaves
// status reporting the unknown string rather than a stale version.
func TestUpdateProxyFailureExitsOneAndRecordsTheFailure(t *testing.T) {
	bin := buildCLI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	home := t.TempDir()
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + home,
		"TPP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + t.TempDir(),
	}, "update")
	if code != 1 {
		t.Fatalf("update against a failing proxy = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "update:") {
		t.Fatalf("the failure must be reported with the update: prefix:\n%s", out)
	}
	t.Setenv("TPP_HOME", home)
	cache, err := update.LoadCache()
	if err != nil {
		t.Fatalf("load cache after a failed check: %v", err)
	}
	if cache.Error == "" || cache.AvailableVersion != "" {
		t.Fatalf("cache after a failed check = %+v, want the error recorded and no version", cache)
	}
	status, statuscode := runCLIWithHomeEnv(t, home, bin, "status", "--json")
	if statuscode != 0 {
		t.Fatalf("status after a failed check = %d\n%s", statuscode, status)
	}
	if got := statusAvailable(t, status); got != unknownAvailableVersion {
		t.Fatalf("status after a failed check = %q, want %q", got, unknownAvailableVersion)
	}
}

// Already current is exit 0 with the versions named, and nothing resembling an install runs.
func TestUpdateReportsWhenAlreadyUpToDate(t *testing.T) {
	bin := buildCLI(t)
	srv := fakeProxy(t, "v"+buildinfo.Version)
	home := t.TempDir()
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + home,
		"TPP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + t.TempDir(),
	}, "update")
	if code != 0 {
		t.Fatalf("update when current = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "already up to date") {
		t.Fatalf("output missing the up-to-date line:\n%s", out)
	}
	if strings.Contains(out, "go install") {
		t.Fatalf("an up-to-date run must not offer an install:\n%s", out)
	}
}

// A module with no release tag reaches the proxy as a pseudo-version, which carries no comparable release
// number: saying "already up to date" would be a verdict the check never reached.
func TestUpdateDoesNotCallAnUncomparableVersionUpToDate(t *testing.T) {
	bin := buildCLI(t)
	srv := fakeProxy(t, "v0.0.0-20260925221235-11e300da863b")
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + t.TempDir(),
		"TPP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + t.TempDir(),
	}, "update", "--check")
	if code != 0 {
		t.Fatalf("update --check on a pseudo-version = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "already up to date") || !strings.Contains(out, "cannot compare") {
		t.Fatalf("an uncomparable latest must be named as such, not as up to date:\n%s", out)
	}
}

// --check refreshes the cache and reports the gap but never reaches for go.
func TestUpdateCheckOnlyNeverInstalls(t *testing.T) {
	bin := buildCLI(t)
	srv := fakeProxy(t, "v99.0.0")
	home := t.TempDir()
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + home,
		"TPP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + t.TempDir(),
	}, "update", "--check")
	if code != 0 {
		t.Fatalf("update --check = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "go install") {
		t.Fatalf("--check must not offer an install:\n%s", out)
	}
	if !strings.Contains(out, "update available") {
		t.Fatalf("--check must report the available update:\n%s", out)
	}
	t.Setenv("TPP_HOME", home)
	cache, err := update.LoadCache()
	if err != nil {
		t.Fatalf("load cache after --check: %v", err)
	}
	if cache.AvailableVersion != "v99.0.0" {
		t.Fatalf("cache = %+v, want --check to refresh it", cache)
	}
}

// A running binary in a directory the user cannot write (a system bin dir) cannot be replaced in place: the
// update refuses before running go, naming the directory, instead of surfacing go's permission error.
func TestUpdateRefusesWhenTheRunningBinaryDirIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only directory")
	}
	src := buildCLI(t)
	roDir := t.TempDir()
	bin := filepath.Join(roDir, "tpp")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })
	srv := fakeProxy(t, "v99.0.0")
	fakeBin := t.TempDir()
	ran := filepath.Join(fakeBin, "ran")
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte("#!/bin/sh\ntouch '"+ran+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + t.TempDir(),
		"TPP_UPDATE_BASE_URL=" + srv.URL,
		"PATH=" + fakeBin,
	}, "update")
	if code != 1 || !strings.Contains(out, "not writable") {
		t.Fatalf("update into a read-only binary dir = %d, want 1 naming it not writable:\n%s", code, out)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatalf("go ran although the running binary's directory is not writable:\n%s", out)
	}
}

// The renamed variable wins when both are set, so a shell that still exports the old name cannot point
// the check at a proxy the operator already moved away from.
func TestUpdateBaseURLPrefersTheNewVariable(t *testing.T) {
	bin := buildCLI(t)
	current := fakeProxy(t, "v99.0.0")
	legacy := fakeProxy(t, "v77.0.0")
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + t.TempDir(),
		"TPP_UPDATE_BASE_URL=" + current.URL,
		"RDD_PLUS_UPDATE_BASE_URL=" + legacy.URL,
		"PATH=" + t.TempDir(),
	}, "update", "--check")
	if code != 0 {
		t.Fatalf("update --check = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "v99.0.0") || strings.Contains(out, "v77.0.0") {
		t.Fatalf("update --check did not use TPP_UPDATE_BASE_URL:\n%s", out)
	}
}

// TSP_UPDATE_BASE_URL is canonical: with all three names exported it wins, so a shell still
// carrying the older TPP_ or RDD_PLUS_ values cannot point the check at a proxy the operator
// already moved away from.
func TestUpdateBaseURLPrefersTSPOverTPPAndRDDPlus(t *testing.T) {
	bin := buildCLI(t)
	canonical := fakeProxy(t, "v99.0.0")
	current := fakeProxy(t, "v88.0.0")
	legacy := fakeProxy(t, "v77.0.0")
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + t.TempDir(),
		"TSP_UPDATE_BASE_URL=" + canonical.URL,
		"TPP_UPDATE_BASE_URL=" + current.URL,
		"RDD_PLUS_UPDATE_BASE_URL=" + legacy.URL,
		"PATH=" + t.TempDir(),
	}, "update", "--check")
	if code != 0 {
		t.Fatalf("update --check = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "v99.0.0") || strings.Contains(out, "v88.0.0") || strings.Contains(out, "v77.0.0") {
		t.Fatalf("update --check did not use TSP_UPDATE_BASE_URL:\n%s", out)
	}
}

// The pre-rename name still points the check when neither newer name is set: a shell that only
// exports RDD_PLUS_UPDATE_BASE_URL keeps working across the rename.
func TestUpdateBaseURLFallsBackToRDDPlusWhenNeitherNewerVariableIsSet(t *testing.T) {
	bin := buildCLI(t)
	legacy := fakeProxy(t, "v77.0.0")
	out, code := runCLIEnv(t, bin, []string{
		"TPP_HOME=" + t.TempDir(),
		"TSP_UPDATE_BASE_URL=",
		"TPP_UPDATE_BASE_URL=",
		"RDD_PLUS_UPDATE_BASE_URL=" + legacy.URL,
		"PATH=" + t.TempDir(),
	}, "update", "--check")
	if code != 0 {
		t.Fatalf("update --check = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "v77.0.0") {
		t.Fatalf("update --check did not fall back to RDD_PLUS_UPDATE_BASE_URL:\n%s", out)
	}
}
