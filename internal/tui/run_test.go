package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

type setCall struct {
	id      string
	enabled bool
}

type spy struct {
	statusErr    error
	featuresErr  error
	setErr       error
	previewErr   error
	planErr      error
	statusCalls  int
	featuresCall int
	planCalls    int
	sets         []setCall
	previews     []string
	statusRows   []FeatureRow // extra features for the status table, nil for the default one
	featureRows  []FeatureRow // rows the features view lists, nil for the default one
	previewText  string       // preview body, default "Preview text for <id>"
}

func (s *spy) deps() Deps {
	return Deps{
		Status: func() (StatusView, error) {
			s.statusCalls++
			if s.statusErr != nil {
				return StatusView{}, s.statusErr
			}
			rows := s.statusRows
			if rows == nil {
				rows = []FeatureRow{{ID: "feedback", Title: "Feedback", Enabled: false}}
			}
			return StatusView{
				StateRoot:        "/tmp/rdd-home",
				StateExists:      true,
				InstalledVersion: "1.4.0",
				AvailableVersion: "unknown (no update check yet)",
				Features:         rows,
			}, nil
		},
		Features: func() ([]FeatureRow, error) {
			s.featuresCall++
			if s.featuresErr != nil {
				return nil, s.featuresErr
			}
			if s.featureRows != nil {
				return s.featureRows, nil
			}
			return []FeatureRow{{ID: "feedback", Title: "Feedback", Enabled: false}}, nil
		},
		SetFeature: func(id string, enabled bool) (string, error) {
			s.sets = append(s.sets, setCall{id: id, enabled: enabled})
			if s.setErr != nil {
				return "", s.setErr
			}
			return "2026-09-22T00:00:00Z", nil
		},
		Preview: func(id string) (string, error) {
			s.previews = append(s.previews, id)
			if s.previewErr != nil {
				return "", s.previewErr
			}
			if s.previewText != "" {
				return s.previewText, nil
			}
			return "Preview text for " + id, nil
		},
		SyncPlan: func() (string, error) {
			s.planCalls++
			if s.planErr != nil {
				return "", s.planErr
			}
			return "plan: dry-run report", nil
		},
	}
}

func runScript(t *testing.T, script string, deps Deps) (string, error) {
	t.Helper()
	t.Setenv("TPP_HOME", t.TempDir())
	var out bytes.Buffer
	err := Run(strings.NewReader(script), &out, deps)
	return out.String(), err
}

func TestRunMenuNavigationAndQuit(t *testing.T) {
	s := &spy{}
	// down, down, ignored space, ignored digit, then q quits.
	out, err := runScript(t, "\x1b[B\x1b[B 4q", s.deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.statusCalls != 0 || s.featuresCall != 0 || s.planCalls != 0 {
		t.Errorf("menu navigation called deps: status=%d features=%d plan=%d",
			s.statusCalls, s.featuresCall, s.planCalls)
	}
	for _, want := range []string{"> Status", "> Features", "> Sync plan", "\x1b[?25l", "\x1b[?25h"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRunStatusView(t *testing.T) {
	s := &spy{}
	// enter opens status, esc back, q quits.
	out, err := runScript(t, "\r\x1bq", s.deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.statusCalls != 1 {
		t.Errorf("Status called %d times, want 1", s.statusCalls)
	}
	for _, want := range []string{"State root: /tmp/rdd-home", "Installed version: 1.4.0", "tsp tui: status"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRunFeaturesToggleAndPreview(t *testing.T) {
	s := &spy{}
	// down to Features, enter, space toggles, enter previews, esc back, q quits.
	out, err := runScript(t, "\x1b[B\r \r\x1bq", s.deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(s.sets) != 1 || s.sets[0] != (setCall{id: "feedback", enabled: true}) {
		t.Errorf("SetFeature calls = %v, want [{feedback true}]", s.sets)
	}
	if len(s.previews) != 1 || s.previews[0] != "feedback" {
		t.Errorf("Preview calls = %v, want [feedback]", s.previews)
	}
	for _, want := range []string{"Preview text for feedback", "> feedback  Feedback  enabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRunSyncPlan(t *testing.T) {
	s := &spy{}
	// down, down to Sync plan, enter, esc, q.
	out, err := runScript(t, "\x1b[B\x1b[B\r\x1bq", s.deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.planCalls != 1 {
		t.Errorf("SyncPlan called %d times, want 1", s.planCalls)
	}
	if !strings.Contains(out, "plan: dry-run report") {
		t.Errorf("output missing dry-run report")
	}
}

func TestRunCtrlCQuits(t *testing.T) {
	s := &spy{}
	if _, err := runScript(t, "\x03", s.deps()); err != nil {
		t.Errorf("Run with ctrl+c: %v, want nil", err)
	}
}

func TestRunDepsErrorsRender(t *testing.T) {
	s := &spy{
		statusErr:  errors.New("status down"),
		setErr:     errors.New("set failed"),
		previewErr: errors.New("preview failed"),
		planErr:    errors.New("plan failed"),
	}
	// status error, features load ok, toggle error, preview error, plan error, quit.
	script := "\r\x1b\x1b[B\r \r\x1b\x1b[B\r\x1bq"
	out, err := runScript(t, script, s.deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"error: status down",
		"error: set failed",
		"error: preview failed",
		"error: plan failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRunFeaturesLoadError(t *testing.T) {
	s := &spy{featuresErr: errors.New("features down")}
	// open features (load fails), space and enter cannot act without rows, esc, q.
	out, err := runScript(t, "\x1b[B\r \r\x1bq", s.deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "error: features down") {
		t.Errorf("output missing %q", "error: features down")
	}
	if len(s.sets) != 0 || len(s.previews) != 0 {
		t.Errorf("toggle/preview ran with no rows: sets=%v previews=%v", s.sets, s.previews)
	}
}

func TestRunInputExhaustedIsNotQuit(t *testing.T) {
	s := &spy{}
	t.Setenv("TPP_HOME", t.TempDir())
	var out bytes.Buffer
	// One ignored key, no quit: input ends and Run reports EOF instead of success.
	err := Run(bytes.NewReader([]byte("x")), &out, s.deps())
	if !errors.Is(err, io.EOF) {
		t.Errorf("Run exhausted input: %v, want io.EOF", err)
	}
}

func TestRunArrowSplitAcrossReads(t *testing.T) {
	s := &spy{}
	var out bytes.Buffer
	in := io.MultiReader(strings.NewReader("\x1b"), strings.NewReader("[B"), strings.NewReader("\r"), strings.NewReader("q"))
	// The injected wait never fires: the held ESC waits for the next read instead of racing a
	// 50ms wall clock, so the test exercises decoding, not the scheduler (issue #135).
	noWait := func(time.Duration) <-chan time.Time { return nil }
	if err := run(in, &out, s.deps(), noWait, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	hasTitle := strings.Contains(out.String(), "tsp tui: features")
	if s.featuresCall != 1 || s.statusCalls != 0 || !hasTitle {
		t.Errorf("features=%d status=%d hasTitle=%v, want 1, 0, true", s.featuresCall, s.statusCalls, hasTitle)
	}
}

func TestRunLoneEscFiresWithoutFollowingInput(t *testing.T) {
	s := &spy{}
	in, w := io.Pipe()
	defer w.Close()
	result := make(chan error, 1)
	go func() { result <- Run(in, io.Discard, s.deps()) }()
	// A real Esc press is one byte and nothing after it; at the menu it quits.
	if _, err := w.Write([]byte("\x1b")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Run after lone esc: %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lone esc was held until more input arrived")
	}
}

func TestRunFramesBreakLinesWithCRLF(t *testing.T) {
	s := &spy{}
	out, err := runScript(t, "\r\x1bq", s.deps())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Raw mode disables output post-processing: a bare LF moves down without returning to column 0.
	if i := strings.Index(strings.ReplaceAll(out, "\r\n", ""), "\n"); i >= 0 {
		t.Errorf("frame has a bare LF at %d: %q", i, out)
	}
}

func TestViewportPinsHelpAndClampsScroll(t *testing.T) {
	lines := []string{"title", "a", "b", "c", "d", "help"}
	got, scroll, page := viewport(lines, 99, 3)
	want := []string{"c", "d", "help | lines 4-5 of 5"}
	if !slices.Equal(got, want) || scroll != 3 || page != 2 {
		t.Errorf("viewport past end = %q scroll=%d page=%d, want %q scroll=3 page=2", got, scroll, page, want)
	}
	if got, scroll, _ := viewport(lines, -4, 3); got[0] != "title" || scroll != 0 {
		t.Errorf("viewport before start = %q scroll=%d, want title first and scroll 0", got, scroll)
	}
	if got, _, _ := viewport(lines, 2, 10); !slices.Equal(got, lines) {
		t.Errorf("viewport that fits = %q, want the frame unchanged", got)
	}
}

// runSized drives a scripted session with a fixed terminal size so viewport and scroll behave
// as they do on a real screen of rows x cols.
func runSized(t *testing.T, script string, deps Deps, rows, cols int) string {
	t.Helper()
	t.Setenv("TPP_HOME", t.TempDir())
	var out bytes.Buffer
	err := run(strings.NewReader(script), &out, deps, time.After, func() (int, int) { return rows, cols })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

// ranges returns every "lines X-Y of N" suffix in draw order, one per frame that carries one
// (menu frames that fit on screen carry none).
func ranges(out string) []string {
	return regexp.MustCompile(`lines \d+-\d+ of \d+`).FindAllString(out, -1)
}

func featureRows(n int) []FeatureRow {
	rows := make([]FeatureRow, n)
	for i := range rows {
		rows[i] = FeatureRow{ID: fmt.Sprintf("f%d", i), Title: fmt.Sprintf("Feature %d", i), Enabled: false}
	}
	return rows
}

func TestRunArrowSplitAcrossReadsIsSchedulerIndependent(t *testing.T) {
	s := &spy{}
	var out bytes.Buffer
	in := io.MultiReader(strings.NewReader("\x1b"), strings.NewReader("[B"), strings.NewReader("\r"), strings.NewReader("q"))
	noWait := func(time.Duration) <-chan time.Time { return nil }
	if err := run(in, &out, s.deps(), noWait, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.featuresCall != 1 || s.statusCalls != 0 {
		t.Errorf("features=%d status=%d, want 1, 0", s.featuresCall, s.statusCalls)
	}
}

func TestRunFeaturesScrollKeepsSelectionAndPreviewReachable(t *testing.T) {
	// rows=8 gives page=7: the features frame is 12 lines without a preview and 21 with the
	// 8-line preview below (body 11 and 20).
	s := &spy{
		featureRows: featureRows(8),
		previewText: strings.Join([]string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8"}, "\n"),
	}
	// down to Features, open, down x5 (selection to f5), preview, pgdn x3, pgup, esc,
	// re-open (offset must reset), quit.
	script := "\x1b[B\r" + strings.Repeat("\x1b[B", 5) + "\r\x1b[6~\x1b[6~\x1b[6~\x1b[5~\x1b\rq"
	out := runSized(t, script, s.deps(), 8, 80)

	want := []string{
		"lines 1-7 of 11",   // open: list frame at the top
		"lines 1-7 of 11",   // down to f1: still inside the page
		"lines 1-7 of 11",   // f2
		"lines 1-7 of 11",   // f3
		"lines 1-7 of 11",   // f4 (body line 6, last inside the page)
		"lines 2-8 of 11",   // f5 leaves the page: the viewport follows the selection
		"lines 1-7 of 20",   // enter loads the preview and resets the offset
		"lines 8-14 of 20",  // pgdn
		"lines 14-20 of 20", // pgdn clamps at the end: the preview bottom is on screen
		"lines 14-20 of 20", // pgdn while clamped
		"lines 7-13 of 20",  // pgup
		"lines 1-7 of 11",   // re-entering features resets the offset
	}
	if got := ranges(out); !slices.Equal(got, want) {
		t.Errorf("frame ranges:\n got: %q\nwant: %q", got, want)
	}
	frames := strings.Split(out, "\x1b[H")
	// frames[1] is the menu draw; each key draws exactly one frame from frames[2] on:
	// key 7 is the fifth down, key 10 the second pgdn.
	if down5 := frames[8]; !strings.Contains(down5, "> f5") {
		t.Errorf("selection at f5 not visible in its frame: %q", down5)
	}
	if paged := frames[11]; !strings.Contains(paged, "line 8") {
		t.Errorf("preview bottom not visible after paging: %q", paged)
	}
	if !strings.Contains(out, "> Features") {
		t.Errorf("esc did not return to the menu")
	}
}

func TestRunScrollKeysThroughRun(t *testing.T) {
	// rows=8 gives page=7; the status frame with 8 features is 18 lines (body 17, max offset 10).
	s := &spy{statusRows: featureRows(8)}
	// open status, down x2, up x3 (clamped), pgdn x2, pgdn (clamped), space (clamped), pgup,
	// esc, re-open (reset), esc, quit.
	script := "\r\x1b[B\x1b[B\x1b[A\x1b[A\x1b[A\x1b[6~\x1b[6~\x1b[6~ \x1b[5~\x1b\r\x1bq"
	out := runSized(t, script, s.deps(), 8, 80)

	want := []string{
		"lines 1-7 of 17",   // open at the top
		"lines 2-8 of 17",   // down
		"lines 3-9 of 17",   // down
		"lines 2-8 of 17",   // up
		"lines 1-7 of 17",   // up
		"lines 1-7 of 17",   // up clamps at the start
		"lines 8-14 of 17",  // pgdn
		"lines 11-17 of 17", // pgdn clamps at the end (max offset 10)
		"lines 11-17 of 17", // pgdn while clamped
		"lines 11-17 of 17", // space pages like pgdn, still clamped
		"lines 4-10 of 17",  // pgup
		"lines 1-7 of 17",   // re-entering status resets the offset
	}
	if got := ranges(out); !slices.Equal(got, want) {
		t.Errorf("frame ranges:\n got: %q\nwant: %q", got, want)
	}
	if s.statusCalls != 2 {
		t.Errorf("Status called %d times, want 2 (open, re-open)", s.statusCalls)
	}
	if !strings.Contains(out, "> Status") {
		t.Errorf("esc did not return to the menu")
	}
}

func TestClipKeepsLinesFromWrapping(t *testing.T) {
	got := clip([]string{"abcdef", "ab"}, 4)
	if !slices.Equal(got, []string{"abc", "ab"}) {
		t.Errorf("clip = %q, want [abc ab]", got)
	}
}
