// Package feedback records one honest process report about the testing discipline itself, sanitizes
// it before it persists, and reads those reports back over time. The gate offers feedback at every
// Stop; this is where the answer lands.
package feedback

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alesierraalta/tpp/internal/assets"
	"github.com/alesierraalta/tpp/internal/feature"
	"github.com/alesierraalta/tpp/internal/sanitize"
)

// Verdicts are the three honest answers to "did the method earn its keep".
const (
	VerdictPaid     = "paid"
	VerdictPartly   = "partly"
	VerdictCeremony = "ceremony"
)

// NotGiven is what a required-but-absent field says instead of guessing a value.
const NotGiven = "(not given)"

// Verdicts lists every accepted verdict in the order a refusal names them.
var Verdicts = []string{VerdictPaid, VerdictPartly, VerdictCeremony}

// Report is one run's feedback, stamped with the run's identity so reports compare across skill
// versions. Every field is required except Guess and Freeform.
type Report struct {
	TS       string `json:"ts"`
	Repo     string `json:"repo"`
	Plan     string `json:"plan"`
	Skill    string `json:"skill"`
	Build    string `json:"build"`
	Paid     string `json:"paid"`
	Cost     string `json:"cost"`
	Reason   string `json:"reason"`
	Verdict  string `json:"verdict"`
	Guess    string `json:"guess,omitempty"`
	Freeform string `json:"freeform,omitempty"`
	// Sanitized is the marker of a row written through the sanitization stage. It is output-only:
	// it is deliberately absent from knownKeys and from Template, so a submitted report cannot
	// claim it. Rows written before the stage existed lack it.
	Sanitized bool `json:"sanitized,omitempty"`
}

func ledgerPathIn(telemetryDir string) string {
	return filepath.Join(telemetryDir, "run-feedback.jsonl")
}

// LedgerPath is the append-only JSONL ledger of reports under configDir.
func LedgerPath(configDir string) string {
	return ledgerPathIn(sanitize.TelemetryDir(configDir))
}

func markdownPathIn(telemetryDir string) string {
	return filepath.Join(telemetryDir, "run-feedback.md")
}

// MarkdownPath is the readable rendering of the same reports under configDir.
func MarkdownPath(configDir string) string {
	return markdownPathIn(sanitize.TelemetryDir(configDir))
}

// requiredFields are the fields a report cannot be recorded without.
var requiredFields = []string{"ts", "repo", "plan", "skill", "build", "paid", "cost", "reason", "verdict"}

// knownKeys is every field the template shape accepts; anything else is a typo, not a new field.
var knownKeys = map[string]bool{
	"ts": true, "repo": true, "plan": true, "skill": true, "build": true,
	"paid": true, "cost": true, "reason": true, "verdict": true, "guess": true, "freeform": true,
}

// keyLine matches `key: value`; a line that does not match continues the previous value.
var keyLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):[ \t]?(.*)$`)

// skillShape accepts a lowercase skill name with an optional dotted numeric version.
var skillShape = regexp.MustCompile(`^[a-z][a-z0-9-]*(?: [0-9]+(?:\.[0-9]+)+)?$`)

// Parse reads the `key: value` template shape: a value runs to the next key, blank lines and
// comment lines are ignored, and an unknown key or an empty required field is a refusal.
func Parse(text string) (Report, error) {
	values := map[string][]string{}
	var order []string
	current := ""
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := keyLine.FindStringSubmatch(line); m != nil {
			current = strings.ToLower(m[1])
			if _, seen := values[current]; !seen {
				order = append(order, current)
			}
			values[current] = append(values[current], strings.TrimSpace(m[2]))
			continue
		}
		if current == "" {
			return Report{}, fmt.Errorf("line %d: %q is not a key: value line", i+1, strings.TrimSpace(line))
		}
		values[current] = append(values[current], strings.TrimSpace(line))
	}
	var unknown []string
	for _, k := range order {
		if !knownKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return Report{}, fmt.Errorf("unknown key(s): %s", strings.Join(unknown, ", "))
	}
	val := func(k string) string { return strings.TrimSpace(strings.Join(values[k], "\n")) }
	r := Report{
		TS: val("ts"), Repo: val("repo"), Plan: val("plan"), Skill: val("skill"), Build: val("build"),
		Paid: val("paid"), Cost: val("cost"), Reason: val("reason"), Verdict: val("verdict"),
		Guess: val("guess"), Freeform: val("freeform"),
	}
	for _, req := range requiredFields {
		if val(req) == "" {
			return Report{}, fmt.Errorf("missing required field: %s", req)
		}
	}
	if !skillShape.MatchString(r.Skill) {
		return Report{}, fmt.Errorf("skill %q must match <name> or <name> <version> (lowercase name, optional dotted numeric version)", r.Skill)
	}
	if !isVerdict(r.Verdict) {
		return Report{}, fmt.Errorf("verdict %q must be one of: %s", r.Verdict, strings.Join(Verdicts, ", "))
	}
	return r, nil
}

// Template renders a fillable skeleton with the run's identity already filled in. It writes
// nothing: the operator edits it and submits it with --file.
func Template(r Report) string {
	plan := r.Plan
	if plan == "" {
		plan = NotGiven
	}
	return strings.Join([]string{
		"# tpp run feedback — what the method cost and what it paid.",
		"# Fill in paid, cost, reason and verdict. verdict is one of: " + strings.Join(Verdicts, ", ") + ".",
		"# Submit with: tpp feedback --file <this file>",
		"ts: " + r.TS,
		"repo: " + r.Repo,
		"plan: " + plan,
		"skill: " + r.Skill,
		"build: " + r.Build,
		"paid: ",
		"cost: ",
		"reason: ",
		"verdict: ",
		"guess: ",
		"freeform: ",
	}, "\n") + "\n"
}

// markdownHeader opens the rendering. It is written only when the file is missing; a report that
// already landed is never rewritten.
const markdownHeader = "# Run feedback\n\nOne section per `tpp feedback` report; never rewritten. Each report grades the method\nitself: what paid off, what was ceremony, and where a rule had to be reverse-engineered.\n\n"

// Record appends one report to the ledger and one section to the markdown file. Both files are
// append-only.
func Record(configDir string, r Report) error {
	enabled, err := feature.Enabled("feedback")
	if err != nil || !enabled {
		if err != nil {
			return fmt.Errorf("feedback is disabled; enable it with: tpp feature enable feedback: %w", err)
		}
		return fmt.Errorf("feedback is disabled; enable it with: tpp feature enable feedback")
	}

	telemetryDir := sanitize.TelemetryDir(configDir)
	key, err := sanitize.LoadKeyIn(telemetryDir)
	if err != nil {
		return fmt.Errorf("load telemetry key: %w", err)
	}

	type mapping struct {
		name, pseudonym, original string
	}
	var mappings []mapping
	if r.Repo != "" {
		original := r.Repo
		r.Repo = key.ID("repo", original)
		mappings = append(mappings, mapping{name: "repo", pseudonym: r.Repo, original: original})
	}
	if r.Plan != "" && r.Plan != NotGiven {
		original := r.Plan
		r.Plan = key.ID("plan", original)
		mappings = append(mappings, mapping{name: "plan", pseudonym: r.Plan, original: original})
	}

	for _, field := range []struct {
		name     string
		value    *string
		required bool
	}{
		{name: "paid", value: &r.Paid, required: true},
		{name: "cost", value: &r.Cost, required: true},
		{name: "reason", value: &r.Reason, required: true},
		{name: "guess", value: &r.Guess},
		{name: "freeform", value: &r.Freeform},
	} {
		*field.value, err = sanitize.Field(*field.value, field.required)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
	}

	for _, field := range []struct {
		name, value string
	}{
		{name: "repo", value: r.Repo}, {name: "plan", value: r.Plan},
		{name: "paid", value: r.Paid}, {name: "cost", value: r.Cost},
		{name: "reason", value: r.Reason}, {name: "guess", value: r.Guess},
		{name: "freeform", value: r.Freeform},
	} {
		if err := sanitize.Verify(field.value); err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
	}
	r.Sanitized = true

	// Every decision above is made in memory, so a refused report leaves no trace: the map is
	// written only for a row that will land. It is still written before the ledger, because
	// recording the row anyway would leave the operator unable to resolve the pseudonym with no
	// signal that it happened, so a map failure stays hard.
	for _, entry := range mappings {
		if err := key.Remember(telemetryDir, entry.pseudonym, entry.original); err != nil {
			return fmt.Errorf("remember %s pseudonym: %w", entry.name, err)
		}
	}

	ledgerPath := ledgerPathIn(telemetryDir)
	markdownPath := markdownPathIn(telemetryDir)
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := appendFile(ledgerPath, append(line, '\n')); err != nil {
		return err
	}
	if _, err := os.Stat(markdownPath); os.IsNotExist(err) {
		if err := appendFile(markdownPath, []byte(markdownHeader)); err != nil {
			return err
		}
	}
	return appendFile(markdownPath, []byte(renderSection(r)))
}

// renderSection is the readable rendering of one report.
func renderSection(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %s\n\n", r.TS, r.Verdict)
	for _, f := range []struct{ name, value string }{
		{"repo", r.Repo}, {"plan", r.Plan}, {"skill", r.Skill}, {"build", r.Build},
		{"paid", r.Paid}, {"cost", r.Cost}, {"reason", r.Reason},
		{"guess", r.Guess}, {"freeform", r.Freeform},
	} {
		if strings.TrimSpace(f.value) == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", f.name, f.value)
	}
	b.WriteString("\n")
	return b.String()
}

func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// Read returns every report in the ledger, oldest first. A missing ledger is empty, not an error.
// Read reads the ledger back for local use. A row written after sanitization carries pseudonyms, so
// the local map resolves them here: the bytes on disk keep no identity, and the operator still reads
// their own project names. A row that predates sanitization is returned exactly as it was written.
func Read(configDir string) ([]Report, error) {
	raw, err := os.ReadFile(LedgerPath(configDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	telemetryDir := sanitize.TelemetryDir(configDir)
	var out []Report
	for i, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r Report
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("ledger line %d: %w", i+1, err)
		}
		if r.Sanitized {
			if original, ok := sanitize.Resolve(telemetryDir, r.Repo); ok {
				r.Repo = original
			}
			if original, ok := sanitize.Resolve(telemetryDir, r.Plan); ok {
				r.Plan = original
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Summary is the readback: counts and the actual words, never a score or an invented theme.
func Summary(configDir string) (string, error) {
	reports, err := Read(configDir)
	if err != nil {
		return "", err
	}
	if len(reports) == 0 {
		return "no reports yet: run `tpp feedback --template` after a run to start the ledger\n", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run feedback: %d report(s)\n", len(reports))
	legacy := 0
	for _, r := range reports {
		if !r.Sanitized {
			legacy++
		}
	}
	if legacy > 0 {
		fmt.Fprintf(&b, "%d of %d report(s) predate sanitization and may carry raw identity\n", legacy, len(reports))
	}
	b.WriteString("\n")

	counts := map[string]int{}
	for _, r := range reports {
		counts[r.Verdict]++
	}
	b.WriteString("verdicts:\n")
	for _, v := range Verdicts {
		fmt.Fprintf(&b, "  %s: %d\n", v, counts[v])
	}

	type split struct{ paid, partly, ceremony, unknown int }
	perSkill := map[string]*split{}
	perProject := map[string]*split{}
	var skills, projects []string
	unknown := 0
	for _, r := range reports {
		s, ok := perSkill[r.Skill]
		if !ok {
			s = &split{}
			perSkill[r.Skill] = s
			skills = append(skills, r.Skill)
		}
		p, ok := perProject[r.Repo]
		if !ok {
			p = &split{}
			perProject[r.Repo] = p
			projects = append(projects, r.Repo)
		}
		switch r.Verdict {
		case VerdictPaid:
			s.paid++
			p.paid++
		case VerdictPartly:
			s.partly++
			p.partly++
		case VerdictCeremony:
			s.ceremony++
			p.ceremony++
		default:
			s.unknown++
			p.unknown++
			unknown++
		}
	}
	if unknown > 0 {
		fmt.Fprintf(&b, "  unknown: %d\n", unknown)
	}

	writeSplit := func(name string, s *split) {
		fmt.Fprintf(&b, "  %s: %d (paid %d, partly %d, ceremony %d",
			name, s.paid+s.partly+s.ceremony+s.unknown, s.paid, s.partly, s.ceremony)
		if s.unknown > 0 {
			fmt.Fprintf(&b, ", unknown %d", s.unknown)
		}
		b.WriteString(")\n")
	}

	sort.Sort(sort.Reverse(sort.StringSlice(skills)))
	b.WriteString("\nby skill version:\n")
	for _, sk := range skills {
		writeSplit(sk, perSkill[sk])
	}

	sort.Sort(sort.Reverse(sort.StringSlice(projects)))
	b.WriteString("\nby project:\n")
	for _, project := range projects {
		writeSplit(project, perProject[project])
	}

	b.WriteString("\nrecent guesses (newest first):\n")
	shown := 0
	for i := len(reports) - 1; i >= 0 && shown < 5; i-- {
		guess := strings.TrimSpace(reports[i].Guess)
		if guess == "" {
			continue
		}
		fmt.Fprintf(&b, "  - %s\n", strings.ReplaceAll(guess, "\n", " "))
		shown++
	}
	if shown == 0 {
		b.WriteString("  (none)\n")
	}
	return b.String(), nil
}

// RepoRoot resolves the git worktree root from dir, falling back to dir when it is not a
// repository, so a report always names the tree it is about.
func RepoRoot(dir string) string {
	out, err := gitAt(dir, "rev-parse", "--show-toplevel")
	root := strings.TrimSpace(out)
	if err == nil && root != "" {
		return root
	}
	if abs, aerr := filepath.Abs(dir); aerr == nil {
		return abs
	}
	return dir
}

func gitAt(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

var skillName = regexp.MustCompile(`(?m)^\s*name:\s*"?([^"\n]+?)"?\s*$`)
var skillVersion = regexp.MustCompile(`(?m)^\s*version:\s*"?([^"\n]+?)"?\s*$`)

// EmbeddedSkillIdentity reads the identity of the embedded test-strategy skill, so a report records
// the skill that produced it and not merely the binary that wrote the row.
func EmbeddedSkillIdentity() string {
	data, err := fs.ReadFile(assets.Skills(), "test-strategy/SKILL.md")
	if err != nil {
		return "unknown"
	}
	nameMatch := skillName.FindSubmatch(data)
	versionMatch := skillVersion.FindSubmatch(data)
	if nameMatch == nil || versionMatch == nil {
		return "unknown"
	}
	name := strings.TrimSpace(string(nameMatch[1]))
	version := strings.TrimSpace(string(versionMatch[1]))
	if name == "" || version == "" {
		return "unknown"
	}
	return name + " " + version
}

func isVerdict(v string) bool {
	for _, want := range Verdicts {
		if v == want {
			return true
		}
	}
	return false
}
