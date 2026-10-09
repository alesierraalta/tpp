// Package tui is the hand-rolled, dependency-free interactive surface: a menu over status,
// feature toggles with preview, and the sync dry-run plan, redrawn as full ANSI frames.
package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Deps is what the CLI injects; tests fake it. The tui package never imports cmd.
type Deps struct {
	Status     func() (StatusView, error)
	Features   func() ([]FeatureRow, error)
	SetFeature func(id string, enabled bool) (string, error) // returns updatedAt
	Preview    func(id string) (string, error)
	SyncPlan   func() (string, error) // dry-run report text; must write nothing
}

// StatusView is the read-only status table the status CLI prints, passed whole.
type StatusView struct {
	StateRoot        string
	StateExists      bool
	InstalledVersion string
	AvailableVersion string // from the update cache; "unknown (no update check yet)" until a check runs
	Features         []FeatureRow
}

// FeatureRow is one registry feature as the CLI lists it.
type FeatureRow struct {
	ID      string
	Title   string
	Enabled bool
}

// errNotTerminal marks an in/out pair that is not a terminal: the loop then runs unmodified so
// tests (and pipes) can drive Run with plain readers.
var errNotTerminal = errors.New("tui: not a terminal")

type view int

const (
	viewMenu view = iota
	viewStatus
	viewFeatures
	viewPlan
)

type app struct {
	deps       Deps
	out        io.Writer
	view       view
	menuSel    int
	featureSel int
	status     StatusView
	statusErr  error
	rows       []FeatureRow
	detail     string
	planReport string
	planErr    error
	scroll     int               // first body line shown when a frame is taller than the terminal
	page       int               // body lines that fit on screen at the last redraw
	size       func() (int, int) // injected terminal geometry; nil queries a real terminal
}

// escWait is how long a held partial escape sequence waits for its continuation before it is
// flushed; a lone Esc press arrives alone, while an arrow's bytes arrive together.
const escWait = 50 * time.Millisecond

type readResult struct {
	b   []byte
	err error
}

// Run drives the menu until quit. It enters raw mode only when in and out are terminal files
// (per-OS helpers; anything else — bytes.Buffer in tests, pipes — skips it), draws on the
// alternate screen with the cursor hidden, and restores the terminal on every exit path via defer.
func Run(in io.Reader, out io.Writer, deps Deps) error {
	return run(in, out, deps, time.After, nil)
}

// run is Run with the two nondeterministic dependencies injected: after schedules the flush of
// a held escape sequence and size reports the terminal geometry (nil keeps the *os.File query).
// Tests pass a timer that never fires and a fixed size so key decoding and scrolling are
// scheduler-independent.
func run(in io.Reader, out io.Writer, deps Deps, after func(time.Duration) <-chan time.Time, size func() (int, int)) error {
	restore, err := beginRaw(in, out)
	if err != nil {
		return err
	}
	if restore != nil {
		defer func() { _ = restore() }()
	}
	if _, err := io.WriteString(out, "\x1b[?1049h\x1b[?25l"); err != nil {
		return err
	}
	defer func() { _, _ = io.WriteString(out, "\x1b[?25h\x1b[?1049l") }()

	a := &app{deps: deps, out: out, size: size}
	if err := a.redraw(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	reads := readLoop(in, done)
	var dec decoder
	for {
		var wait <-chan time.Time
		if dec.holding() {
			wait = after(escWait)
		}
		var keys []key
		var readErr error
		select {
		case r := <-reads:
			readErr = r.err
			keys = dec.decode(r.b)
			if readErr != nil {
				keys = append(keys, dec.flush()...)
			}
		case <-wait:
			keys = dec.flush()
		}
		for _, k := range keys {
			if a.handle(k) {
				return nil
			}
			if err := a.redraw(); err != nil {
				return err
			}
		}
		if readErr != nil {
			return readErr
		}
	}
}

// readLoop reads in on its own goroutine so the loop can time out a held escape sequence; it
// stops after the first read error or once Run returns.
func readLoop(in io.Reader, done <-chan struct{}) <-chan readResult {
	reads := make(chan readResult)
	go func() {
		for {
			buf := make([]byte, 256)
			n, err := in.Read(buf)
			select {
			case reads <- readResult{b: buf[:n], err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return reads
}

// beginRaw enters raw mode when both streams are terminal files; plain readers and non-terminal
// files skip it, and an unsupported platform with a real terminal reports that instead.
func beginRaw(in io.Reader, out io.Writer) (func() error, error) {
	inFile, ok := in.(*os.File)
	if !ok {
		return nil, nil
	}
	outFile, ok := out.(*os.File)
	if !ok {
		return nil, nil
	}
	restore, err := makeRaw(inFile, outFile)
	if errors.Is(err, errNotTerminal) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return restore, nil
}

// handle applies one key to the current view and reports whether the loop should quit. q and
// ctrl+c quit from anywhere; esc at the menu quits because the root has no parent.
func (a *app) handle(k key) bool {
	if k == keyQ || k == keyCtrlC {
		return true
	}
	switch a.view {
	case viewMenu:
		return a.handleMenu(k)
	case viewFeatures:
		return a.handleFeatures(k)
	default:
		a.handleScroll(k)
		return false
	}
}

// handleScroll moves the read-only views (status, sync plan) through a body taller than the
// screen; redraw clamps the offset to what the frame actually has.
func (a *app) handleScroll(k key) {
	switch k {
	case keyEsc:
		a.view = viewMenu
	case keyUp:
		a.scroll--
	case keyDown:
		a.scroll++
	case keyPgUp:
		a.scroll -= max(a.page, 1)
	case keyPgDn, keySpace:
		a.scroll += max(a.page, 1)
	}
}

func (a *app) handleMenu(k key) bool {
	switch k {
	case keyUp:
		if a.menuSel > 0 {
			a.menuSel--
		}
	case keyDown:
		if a.menuSel < len(menuItems)-1 {
			a.menuSel++
		}
	case keyEsc:
		return true
	case keyEnter:
		a.scroll = 0
		switch a.menuSel {
		case 0:
			a.view = viewStatus
			a.status, a.statusErr = a.deps.Status()
		case 1:
			a.enterFeatures()
		case 2:
			a.view = viewPlan
			// The dry run walks every managed file; acknowledge the key before it blocks.
			_ = writeFrame(a.out, []string{planTitle, "", "computing the sync plan..."})
			a.planReport, a.planErr = a.deps.SyncPlan()
		default:
			return true
		}
	}
	return false
}

func (a *app) enterFeatures() {
	a.view = viewFeatures
	a.detail = ""
	a.featureSel = 0
	rows, err := a.deps.Features()
	if err != nil {
		a.rows = nil
		a.detail = "error: " + err.Error()
		return
	}
	a.rows = rows
}

func (a *app) handleFeatures(k key) bool {
	switch k {
	case keyUp:
		if a.featureSel > 0 {
			a.featureSel--
			a.followSelection()
		}
	case keyDown:
		if a.featureSel < len(a.rows)-1 {
			a.featureSel++
			a.followSelection()
		}
	case keyPgUp:
		a.scroll -= max(a.page, 1)
	case keyPgDn:
		a.scroll += max(a.page, 1)
	case keyEsc:
		a.view = viewMenu
	case keySpace:
		if a.featureSel >= len(a.rows) {
			return false
		}
		row := &a.rows[a.featureSel]
		enabled := !row.Enabled
		if _, err := a.deps.SetFeature(row.ID, enabled); err != nil {
			a.detail = "error: " + err.Error()
			return false
		}
		row.Enabled = enabled
	case keyEnter:
		if a.featureSel >= len(a.rows) {
			return false
		}
		text, err := a.deps.Preview(a.rows[a.featureSel].ID)
		if err != nil {
			a.detail = "error: " + err.Error()
			return false
		}
		a.detail = text
		// The preview rewrites the whole frame: show its top instead of resuming the old offset.
		a.scroll = 0
	}
	return false
}

// followSelection moves the offset so the selected row stays inside the visible page. The
// features frame is title, blank, then one line per row, so the selection sits at body line
// 2+featureSel; redraw clamps the offset against the frame the last draw produced.
func (a *app) followSelection() {
	line := 2 + a.featureSel
	switch {
	case line < a.scroll:
		a.scroll = line
	case a.page > 0 && line >= a.scroll+a.page:
		a.scroll = line - a.page + 1
	}
}

func (a *app) frame() string {
	switch a.view {
	case viewStatus:
		return renderStatus(a.status, a.statusErr)
	case viewFeatures:
		return renderFeatures(a.rows, a.featureSel, a.detail)
	case viewPlan:
		return renderPlan(a.planReport, a.planErr)
	default:
		return renderMenu(a.menuSel)
	}
}

func (a *app) redraw() error {
	rows, cols := 0, 0
	switch {
	case a.size != nil:
		rows, cols = a.size()
	default:
		if f, ok := a.out.(*os.File); ok {
			rows, cols = termSize(f)
		}
	}
	lines := strings.Split(a.frame(), "\n")
	lines, a.scroll, a.page = viewport(lines, a.scroll, rows)
	return writeFrame(a.out, clip(lines, cols))
}

// viewport keeps a frame within rows terminal lines: the last line (the key help) stays pinned
// and the body above it shows from offset scroll, with the visible range appended to the help.
// rows <= 0 means the size is unknown and the frame is returned whole. It returns the lines to
// draw, the clamped offset, and how many body lines fit.
func viewport(lines []string, scroll, rows int) ([]string, int, int) {
	if rows <= 0 || len(lines) <= rows {
		return lines, 0, len(lines)
	}
	body, help := lines[:len(lines)-1], lines[len(lines)-1]
	page := max(rows-1, 1)
	scroll = min(max(scroll, 0), len(body)-page)
	shown := append(append([]string(nil), body[scroll:scroll+page]...),
		fmt.Sprintf("%s | lines %d-%d of %d", help, scroll+1, scroll+page, len(body)))
	return shown, scroll, page
}

// clip cuts each line to cols runes so a long line never wraps and pushes the frame off screen;
// cols <= 0 means the width is unknown.
func clip(lines []string, cols int) []string {
	if cols <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if r := []rune(line); len(r) >= cols {
			line = string(r[:cols-1])
		}
		out[i] = line
	}
	return out
}

// writeFrame repaints the whole screen from the top-left corner. Raw mode turns off output
// post-processing, so every line break is an explicit CRLF: a bare LF would only move down and
// draw each line further right than the last.
func writeFrame(out io.Writer, lines []string) error {
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(line)
		b.WriteString("\x1b[K")
	}
	b.WriteString("\x1b[J")
	_, err := io.WriteString(out, b.String())
	return err
}
