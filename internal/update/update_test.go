package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alesierraalta/tpp/internal/buildinfo"
)

func useHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "tpp")
	t.Setenv("TPP_HOME", home)
	return home
}

// The check must hit the module's @latest endpoint and answer how the running build stands
// against the tag the proxy reports, with the v-prefix normalized away.
func TestCheckAnswersHowTheInstalledBuildStands(t *testing.T) {
	original := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = original })
	cases := []struct {
		name      string
		installed string
		served    string
		want      Relation
	}{
		{name: "behind a newer tag", installed: "1.2.3", served: "v1.2.4", want: Behind},
		{name: "equal with and without the v prefix", installed: "1.2.3", served: "v1.2.3", want: Equal},
		{name: "installed ahead of the proxy", installed: "1.3.0", served: "v1.2.9", want: Equal},
		{name: "an installed version that cannot be compared", installed: "dev", served: "v1.0.0", want: Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buildinfo.Version = tc.installed
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/github.com/alesierraalta/tsp/@latest" {
					t.Errorf("proxy path = %q, want the module's @latest endpoint", r.URL.Path)
				}
				fmt.Fprintf(w, `{"Version":%q}`, tc.served)
			}))
			defer srv.Close()
			result, err := (Checker{BaseURL: srv.URL}).Check(context.Background())
			if err != nil {
				t.Fatalf("Check returned an error: %v", err)
			}
			if result.Latest != tc.served {
				t.Fatalf("Latest = %q, want %q", result.Latest, tc.served)
			}
			if result.Relation != tc.want {
				t.Fatalf("Relation = %v, want %v", result.Relation, tc.want)
			}
		})
	}
}

// Every way the proxy can fail — a bad body, an empty payload, a non-200 status — is one typed
// error the CLI can report and cache, never a panic and never a half-parsed version.
func TestCheckReportsEveryProxyFailureAsErrCheck(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "malformed JSON", status: http.StatusOK, body: "{not json"},
		{name: "a payload with no version", status: http.StatusOK, body: `{"Version":""}`},
		{name: "a non-200 answer", status: http.StatusInternalServerError, body: "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			_, err := (Checker{BaseURL: srv.URL}).Check(context.Background())
			if !errors.Is(err, ErrCheck) {
				t.Fatalf("err = %v, want it wrapped in ErrCheck", err)
			}
		})
	}
}

// A proxy that never answers must not hang the command: the client timeout turns the stall into
// the same typed error every other failure becomes.
func TestCheckBoundsAProxyThatNeverAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	checker := Checker{BaseURL: srv.URL, HTTP: &http.Client{Timeout: 50 * time.Millisecond}}
	_, err := checker.Check(context.Background())
	if !errors.Is(err, ErrCheck) {
		t.Fatalf("err = %v, want it wrapped in ErrCheck", err)
	}
}

// The cache mirrors state.Save's contract: an identical result re-saved leaves the file
// byte-identical, a failed check lands as an error entry, and no check yet reads as empty.
func TestCacheRoundTripWritesOnlyWhenTheBytesChange(t *testing.T) {
	home := useHome(t)
	path := filepath.Join(home, "update-check.json")
	entry := Cache{AvailableVersion: "v1.4.0", CheckedAt: "2026-09-22T12:00:00Z"}
	wrote, err := SaveCache(entry)
	if err != nil || !wrote {
		t.Fatalf("first SaveCache = (%v, %v), want (true, nil)", wrote, err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	wrote, err = SaveCache(entry)
	if err != nil || wrote {
		t.Fatalf("second SaveCache = (%v, %v), want (false, nil)", wrote, err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read cache: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("second SaveCache changed cache bytes: before %q, after %q", first, second)
	}
	loaded, err := LoadCache()
	if err != nil {
		t.Fatalf("LoadCache returned an error: %v", err)
	}
	if loaded != entry {
		t.Fatalf("LoadCache = %+v, want %+v", loaded, entry)
	}

	failed := Cache{CheckedAt: "2026-09-22T12:05:00Z", Error: "proxy unreachable"}
	if _, err := SaveCache(failed); err != nil {
		t.Fatalf("SaveCache of a failed check: %v", err)
	}
	loaded, err = LoadCache()
	if err != nil {
		t.Fatalf("LoadCache after a failed check: %v", err)
	}
	if loaded.Error == "" || loaded.AvailableVersion != "" {
		t.Fatalf("failed check cache = %+v, want the error recorded and no version", loaded)
	}
}

func TestLoadCacheWithoutACheckIsEmpty(t *testing.T) {
	useHome(t)
	entry, err := LoadCache()
	if err != nil {
		t.Fatalf("LoadCache returned an error for a missing cache: %v", err)
	}
	if entry != (Cache{}) {
		t.Fatalf("LoadCache missing = %+v, want the zero cache", entry)
	}
}

// The printed line is the contract an offline machine depends on: it has to be exactly the
// command `go` would run, so a user can copy it without editing.
func TestInstallCommandIsTheExactGoInstallLine(t *testing.T) {
	got := InstallCommand("v1.2.3")
	want := "go install github.com/alesierraalta/tsp/cmd/tsp@v1.2.3"
	if got != want {
		t.Fatalf("InstallCommand = %q, want %q", got, want)
	}
}

// A machine without go must be told to run the command instead of having anything executed.
func TestRunInstallWithoutGoReportsErrGoMissingAndRunsNothing(t *testing.T) {
	ran := false
	err := RunInstall("v1.2.3",
		func(string) (string, error) { return "", errors.New("not found") },
		func(string, ...string) error { ran = true; return nil },
	)
	if !errors.Is(err, ErrGoMissing) {
		t.Fatalf("err = %v, want ErrGoMissing", err)
	}
	if ran {
		t.Fatal("RunInstall ran the install although go is absent")
	}
}

// When go is present the argv handed to the runner is the printed command, word for word:
// one source for what is executed and what an offline user is told to run.
func TestRunInstallHandsTheRunnerThePrintedCommand(t *testing.T) {
	var name string
	var args []string
	err := RunInstall("v1.2.3",
		func(string) (string, error) { return "/usr/bin/go", nil },
		func(n string, a ...string) error {
			name, args = n, a
			return nil
		},
	)
	if err != nil {
		t.Fatalf("RunInstall returned an error: %v", err)
	}
	joined := strings.Join(append([]string{name}, args...), " ")
	if joined != InstallCommand("v1.2.3") {
		t.Fatalf("runner argv = %q, want %q", joined, InstallCommand("v1.2.3"))
	}
}

// An uncomparable pair names the side that carries no release number, so the operator is not told to tag a
// release that exists when it is the local build that has no version.
func TestUncomparableNamesTheSideThatIsNotARelease(t *testing.T) {
	for _, tc := range []struct{ installed, latest, want string }{
		{"0.3.12", "v0.0.0-20260925221235-11e300da863b", "latest v0.0.0-20260925221235-11e300da863b is not a release version"},
		{"dev", "v0.3.13", "installed dev is not a release version"},
	} {
		if got := Uncomparable(tc.installed, tc.latest); !strings.Contains(got, tc.want) {
			t.Errorf("Uncomparable(%q, %q) = %q, want it to contain %q", tc.installed, tc.latest, got, tc.want)
		}
	}
}
