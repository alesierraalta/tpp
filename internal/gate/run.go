package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alesierraalta/tsp/internal/feature"
	"github.com/alesierraalta/tsp/internal/sanitize"
)

const gitTimeout = 10 * time.Second

var feedbackOfferState = struct {
	sync.Mutex
	enabled atomic.Bool
}{}

func init() {
	feedbackOfferState.enabled.Store(true)
}

func feedbackOfferEnabled() bool {
	return feedbackOfferState.enabled.Load()
}

func decideForRun(in Input, d Deps, offerFeedback bool) (result Result) {
	feedbackOfferState.Lock()
	defer func() {
		feedbackOfferState.enabled.Store(true)
		feedbackOfferState.Unlock()
	}()
	feedbackOfferState.enabled.Store(offerFeedback)
	return Decide(in, d)
}

func realGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	return string(out), err
}

// RealDeps wires the process boundaries to the real git, filesystem, and clock.
func RealDeps(now time.Time) Deps {
	wd, _ := os.Getwd()
	return Deps{
		Git:  realGit,
		Stat: os.Stat,
		OpenTranscript: func(path string) (io.ReadCloser, error) {
			return os.Open(path)
		},
		Now:     now,
		WorkDir: wd,
	}
}

// DefaultLogPath resolves the telemetry log: env override, else <configDir>/telemetry.
func DefaultLogPath(configDir string) string {
	if p := os.Getenv("TESTING_GATE_LOG"); p != "" {
		return p
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configDir = filepath.Join(home, ".claude")
	}
	return filepath.Join(sanitize.TelemetryDir(configDir), "testing-gate.jsonl")
}

// unreadablePayloadEntry is the line a hook leaves when it could not read what it was handed: it ran, and it
// decided nothing, which is a different fact from a hook that never ran at all.
func unreadablePayloadEntry(now time.Time) *Entry {
	return &Entry{TS: now.UTC().Format(time.RFC3339), SkillsLoaded: []string{}, Skipped: "unreadable_payload"}
}

// appendEntry never fails the turn: a lost log line is cheaper than a broken session.
func appendEntry(path string, e *Entry) {
	if path == "" {
		return
	}
	telemetryDir := filepath.Dir(path)
	if err := os.MkdirAll(telemetryDir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	e = sealEntry(telemetryDir, e)
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}

func sealEntry(telemetryDir string, e *Entry) *Entry {
	if e == nil {
		return nil
	}
	sealed := *e
	if e.SkillsLoaded != nil {
		sealed.SkillsLoaded = make([]string, len(e.SkillsLoaded))
		copy(sealed.SkillsLoaded, e.SkillsLoaded)
	}
	key, err := sanitize.LoadKeyIn(telemetryDir)
	if err != nil {
		// Drop identity rather than write it raw; decision fields survive on purpose so the row remains useful.
		sealed.Repo = ""
		sealed.Session = ""
		sealed.Plan = ""
		return &sealed
	}
	remember := func(prefix, original string) string {
		pseudonym := key.ID(prefix, original)
		// The map is a local operator aid; a map failure must not lose the usable measurement or its invocation rate.
		_ = key.Remember(telemetryDir, pseudonym, original)
		return pseudonym
	}
	if e.Repo != "" {
		sealed.Repo = remember("repo", e.Repo)
	}
	if e.Session != "" {
		sealed.Session = remember("sess", e.Session)
	}
	if e.Plan != "" {
		sealed.Plan = remember("plan", e.Plan)
	}
	return &sealed
}

func emit(w io.Writer, reason string) { emitWith(w, reason, "") }

// emitWith sends the reason to the model as context and, when there is one, a line the operator
// reads in their own terminal: the offer of feedback must not depend on the model relaying it.
func emitWith(w io.Writer, reason, userLine string) {
	payload := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     "Stop",
			"additionalContext": reason,
		},
	}
	if userLine != "" {
		payload["systemMessage"] = userLine
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
	_, _ = w.Write(bytes.TrimRight(buf.Bytes(), "\n"))
}

// Run is the Stop hook: stdin payload → decision → log line → optional Stop feedback.
// It always returns 0; an unreadable payload or an internal failure is silence, never a
// broken turn.
func Run(stdin io.Reader, stdout io.Writer, logPath string, now time.Time) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = 0
		}
	}()
	raw, _ := io.ReadAll(stdin)
	payload := strings.TrimSpace(string(raw))
	// A pointer, not a struct: the JSON literal `null` unmarshals into a zero value without an error, and a
	// hook that took that for a readable payload would decide against a repository nobody named.
	var parsed *Input
	// An empty payload and a malformed one are the same fact to this hook: it ran and could not read what it
	// was handed. One label says that once, instead of inventing a difference the reader cannot act on.
	if payload == "" || json.Unmarshal([]byte(payload), &parsed) != nil || parsed == nil {
		// A payload the hook cannot read is a decision it has to record: it ran, and it decided nothing. The
		// log is the only place that distinction survives — the turn itself must never break.
		appendEntry(logPath, unreadablePayloadEntry(now))
		return 0
	}
	in := *parsed
	offerFeedback, err := feature.Enabled("feedback")
	if err != nil {
		offerFeedback = false
	}
	deps := RealDeps(now)
	// The stored-binding directory is derived from the same log path this hook writes, so what
	// `tpp bind` stores beside the log and what Stop reads from it are one derivation.
	deps.BindingDir = BindingsDir(logPath)
	res := decideForRun(in, deps, offerFeedback)
	if res.Entry != nil {
		appendEntry(logPath, res.Entry)
	}
	switch {
	case res.Fire:
		emit(stdout, res.Reason)
	case res.Audit:
		emitWith(stdout, res.Reason, auditLine(res, offerFeedback))
	case res.Problem != "":
		// A declaration the gate cannot read is a defect its operator can repair, and the model can too:
		// both hear it. It is not an audit, so no counts ride along.
		emitWith(stdout, res.Problem, res.Problem)
	}
	return 0
}

// auditLine is what the operator sees without the model saying anything. It renders the decision, not
// the reason text: breadth is owed by layers, by ranked targets, or by both, and a run that swept every
// layer it planned still owes if its ranked targets are pending.
func auditLine(res Result, offerFeedback ...bool) string {
	suffix := ""
	includeOffer := feedbackOfferEnabled()
	if len(offerFeedback) > 0 {
		includeOffer = offerFeedback[0]
	}
	if includeOffer {
		suffix = " Want feedback on this run?"
	}
	switch {
	case res.RunProblem != "":
		// The bound run's scope could not be read: the operator hears the scoped problem itself,
		// actionable in one line — never "owes nothing", which the unreadable scope cannot prove.
		return "tpp: " + res.RunProblem + suffix
	case res.Owed > 0 && res.Pending > 0:
		return fmt.Sprintf("tpp: %d layer(s) assigned and never invoked, %d ranked target(s) still pending.%s", res.Owed, res.Pending, suffix)
	case res.Owed > 0:
		return fmt.Sprintf("tpp: %d layer(s) assigned and never invoked.%s", res.Owed, suffix)
	case res.Pending > 0:
		return fmt.Sprintf("tpp: %d ranked target(s) still pending.%s", res.Pending, suffix)
	case res.Unreadable > 0:
		return fmt.Sprintf("tpp: %d breadth table(s) could not be read to the end, so the rows under it were never counted.%s", res.Unreadable, suffix)
	case res.Unplanned:
		return "tpp: the plan has no layer matrix, so the breadth sweep was never planned." + suffix
	case res.Micro:
		return "tpp: the micro plan owes nothing; it owes no layer sweep." + suffix
	default:
		return "tpp: the testing plan owes nothing." + suffix
	}
}
