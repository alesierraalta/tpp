package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveLoadRunPreservesMetadataCaseRunsAndSeparateEventLogs(t *testing.T) {
	var log Log
	if _, err := log.Append(Event{
		Entity: EntityFinding, ID: "f1", Kind: EventAdmit,
		PreviousState: string(FindingRaw), NewState: string(FindingPendingAdjudication), TS: "t1",
	}); err != nil {
		t.Fatal(err)
	}
	stored := StoredRun{
		K: 1, HarnessLabel: "baseline", SourceResultsDir: "/results",
		Record: RunRecord{
			Data: RunData{ID: "run-1"}, State: RunAdjudicating,
			ManifestSHA256: "manifest", PolicySHA256: "policy", HarnessID: "harness",
			Model: "model", Runner: "pi", Environment: "linux/amd64",
		},
		CaseRuns: map[string]CaseRun{"case-a": {Case: "case-a", Findings: []Finding{{ID: "f1", Row: 1}}, Log: log}},
	}
	runDir := filepath.Join(t.TempDir(), "run-1")
	if err := SaveRun(runDir, stored); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(runDir, "run.json"),
		filepath.Join(runDir, "case-a", "caserun.json"),
		filepath.Join(runDir, "case-a", "events.jsonl"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing persisted file %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"k": 1`, `"state": "ADJUDICATING"`, `"harness_label": "baseline"`, `"source_results_dir": "/results"`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("run.json missing %s: %s", field, data)
		}
	}
	loaded, err := LoadRun(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.K != stored.K || loaded.HarnessLabel != stored.HarnessLabel || loaded.Record.State != stored.Record.State || loaded.Record.HarnessID != stored.Record.HarnessID {
		t.Fatalf("loaded metadata = %+v", loaded)
	}
	if !reflect.DeepEqual(loaded.CaseRuns["case-a"].Findings, stored.CaseRuns["case-a"].Findings) {
		t.Fatalf("loaded findings = %+v", loaded.CaseRuns["case-a"].Findings)
	}
	if len(loaded.CaseRuns["case-a"].Log.Events) != 1 || loaded.CaseRuns["case-a"].Log.Events[0].Hash != log.Events[0].Hash || loaded.CaseRuns["case-a"].Log.Verify() != nil {
		t.Fatalf("loaded event log = %+v", loaded.CaseRuns["case-a"].Log)
	}
	digest, err := Digest(filepath.Join(runDir, "run.json"))
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("Digest() = %q, %v", digest, err)
	}
}

// confirmedClosedCase builds a closed case whose full-match primary received a confirmation.
func confirmedClosedCase(t *testing.T, outcome ReproOutcome) CaseRun {
	t.Helper()
	cr := CaseRun{
		Case: "case",
		Issues: []Issue{{Case: "case", ID: "D1", Domain: Correctness, Severity: High,
			Reproduction: Reproduction{Applies: true, Oracle: "oracle", Attempts: 3}}},
		Findings: []Finding{
			{ID: "f1", Row: 1, ReportedSeverityText: "high", Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
			{ID: "f2", Row: 2, ReportedSeverityText: "high", Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
		},
	}
	if err := cr.Admit("t1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"f1", "f2"} {
		if err := cr.Decide(Decision{FindingID: id, CandidateIssue: "D1", C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cr.RecordReproductionConfirmation("D1", string(outcome), confirmDigestA, 3, "t3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.Close("t4"); err != nil {
		t.Fatal(err)
	}
	return cr
}

// Without confirmation events reproduction stays the admission C5 fold for every candidate finding.
func TestReproFromEventsLegacyFallbackWithoutConfirmations(t *testing.T) {
	cr := CaseRun{
		Case: "case",
		Issues: []Issue{
			{Case: "case", ID: "D1", Reproduction: Reproduction{Applies: true, Oracle: "oracle"}},
			{Case: "case", ID: "D3", Reproduction: Reproduction{Applies: false}},
		},
		Findings: []Finding{
			{ID: "f1", Row: 1, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
			{ID: "f2", Row: 2, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactFalse}}},
			{ID: "f3", Row: 3, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactNA}}},
			{ID: "f4", Row: 4, Computed: map[string]ComputedFacts{"D1": {C2: FactTrue, C4Cited: FactTrue, C5: FactUnknown}}},
			{ID: "f5", Row: 5, Computed: map[string]ComputedFacts{"D3": {C2: FactTrue, C4Cited: FactTrue, C5: FactTrue}}},
		},
	}
	if err := cr.Admit("t1"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"f1", "D1"}, {"f2", "D1"}, {"f3", "D1"}, {"f4", "D1"}, {"f5", "D3"}} {
		if err := cr.Decide(Decision{FindingID: pair[0], CandidateIssue: pair[1], C1: FactTrue, C3: FactTrue, C4Shows: FactTrue, By: "reviewer", TS: "t2", Reason: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cr.Close("t3"); err != nil {
		t.Fatal(err)
	}
	want := map[string]ReproOutcome{
		"f1": Reproduced,
		"f2": NotReproduced,
		"f3": NotApplicable,
		"f4": NotRun,
		"f5": NotApplicable,
	}
	if got := ReproFromEvents(cr); !reflect.DeepEqual(got, want) {
		t.Fatalf("ReproFromEvents() = %v, want legacy C5 fold %v", got, want)
	}
}

// A recorded confirmation replaces the C5 fold for the Issue's primary finding only.
func TestReproFromEventsConfirmationMapsToPrimaryOnly(t *testing.T) {
	for _, tt := range []struct {
		outcome ReproOutcome
		want    ReproOutcome
	}{
		{outcome: Reproduced, want: Reproduced},
		{outcome: NotReproduced, want: NotReproduced},
	} {
		t.Run(string(tt.outcome), func(t *testing.T) {
			cr := confirmedClosedCase(t, tt.outcome)
			want := map[string]ReproOutcome{"f1": tt.want}
			if got := ReproFromEvents(cr); !reflect.DeepEqual(got, want) {
				t.Fatalf("ReproFromEvents() = %v, want primary-only %v (duplicate f2 excluded)", got, want)
			}
		})
	}
}

// A completed run with confirmations reconstructs from disk, and its record must match the event fold.
func TestConfirmedRunReconstructsFromPersistedEvents(t *testing.T) {
	cr := confirmedClosedCase(t, NotReproduced)
	wantData := RunData{ID: "run-1", Cases: []CaseResult{{
		Case:             "case",
		Issues:           append([]Issue(nil), cr.Issues...),
		IssueStates:      map[string]IssueState{"D1": IssuePD},
		Primary:          map[string]string{"D1": "f1"},
		FindingStates:    map[string]FindingState{"f1": FindingPDFinding, "f2": FindingDuplicate},
		ReportedSeverity: map[string]Severity{"f1": High, "f2": High},
		Repro:            map[string]ReproOutcome{"f1": NotReproduced},
	}}}
	stored := StoredRun{
		K: 1, WeightedRecallW: 0.5,
		Record:        RunRecord{State: RunCompleted, ManifestSHA256: "manifest-digest", Data: wantData},
		CaseRuns:      map[string]CaseRun{"case": cr},
		CaseOutcomes:  map[string]string{"case": ""},
		CaseControls:  map[string]bool{"case": false},
		CaseResources: map[string]CaseRunResources{"case": {}},
	}
	runDir := filepath.Join(t.TempDir(), "run-1")
	if err := SaveRun(runDir, stored); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRun(runDir)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ManifestSHA256: "manifest-digest", MetricConfig: MetricConfig{WeightedRecallW: 0.5}, Cases: []ManifestCase{{ID: "case"}}}
	data, err := ReconstructCompletedRun(loaded, manifest)
	if err != nil {
		t.Fatalf("confirmed run must reconstruct from persisted events: %v", err)
	}
	if !reflect.DeepEqual(data.Cases[0].Repro, map[string]ReproOutcome{"f1": NotReproduced}) {
		t.Fatalf("Repro = %v, want primary-only confirmation outcome", data.Cases[0].Repro)
	}
	loaded.Record.Data.Cases[0].Repro = map[string]ReproOutcome{"f1": Reproduced}
	if _, err := ReconstructCompletedRun(loaded, manifest); err == nil {
		t.Fatal("reconstruction accepted a record detached from its confirmation events")
	}
}

// A malformed confirmation never overrides the valid recorded outcome.
func TestReproFromEventsIgnoresMalformedConfirmation(t *testing.T) {
	cr := confirmedClosedCase(t, Reproduced)
	craftConfirm(t, &cr.Log, Event{Entity: EntityIssue, ID: "case/D1", Kind: EventConfirm, PreviousState: string(IssueUnderEvaluation), NewState: string(IssueUnderEvaluation), TS: "t5", Adjudicator: ConfirmAdjudicator, Payload: json.RawMessage(`{"issue_id":"case/D1","outcome":"MAYBE","artifact_digest":"","attempts":-7}`)})
	want := map[string]ReproOutcome{"f1": Reproduced}
	if got := ReproFromEvents(cr); !reflect.DeepEqual(got, want) {
		t.Fatalf("ReproFromEvents() = %v, want %v: a malformed confirmation must be ignored", got, want)
	}
	if outcome, ok := cr.latestConfirmation(cr.Issues[0]); !ok || outcome != Reproduced {
		t.Fatalf("latestConfirmation = %q, %v; want the latest valid confirmation %q", outcome, ok, Reproduced)
	}
}

// promoteNovelFixtures records proofs and controlled promotions for f1 (applicable) and
// f2 (non-applicable conformance), plus f3's historical direct CONFIRMED_NOVEL decide.
func promoteNovelFixtures(t *testing.T, cr *CaseRun) {
	t.Helper()
	byID := func(id string) Finding {
		finding, ok := cr.finding(id)
		if !ok {
			t.Fatalf("finding %q missing", id)
		}
		return *finding
	}
	if err := cr.RecordNovelProof(novelProofFor(byID("f1")), "3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.ConfirmNovel("f1", "4"); err != nil {
		t.Fatal(err)
	}
	conformance := novelProofFor(byID("f2"))
	conformance.ProofKind = NovelProofConformance
	conformance.ProofRule = "novel-conformance@1"
	conformance.Reproduction = NovelReproduction{Applies: false, Outcome: NotApplicable, ArtifactDigest: confirmDigestB}
	if err := cr.RecordNovelProof(conformance, "3"); err != nil {
		t.Fatal(err)
	}
	if err := cr.ConfirmNovel("f2", "4"); err != nil {
		t.Fatal(err)
	}
	decision := Decision{FindingID: "f3", UnmatchedOutcome: FindingConfirmedNovel, By: "reviewer", TS: "3", Reason: "verified novel"}
	payload, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cr.Log.Append(Event{Entity: EntityFinding, ID: "f3", Kind: EventDecide,
		PreviousState: string(FindingNovelCandidate), NewState: string(FindingConfirmedNovel),
		Reason: decision.Reason, TS: decision.TS, Adjudicator: decision.By, Payload: payload}); err != nil {
		t.Fatalf("append historical novel decide: %v", err)
	}
}

// Proof-promoted novelty derives reproduction only from a valid referenced proof:
// applicable proofs reproduce, conformance non-applicable proofs are NotApplicable, and
// historical or reference-tampered promotions stay absent (NotRun), never credited.
func TestReproFromEventsCountsOnlyProofPromotedNovelty(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1", "f2", "f3", "f4")
	promoteNovelFixtures(t, cr)
	f4, ok := cr.finding("f4")
	if !ok {
		t.Fatal("finding f4 missing")
	}
	if err := cr.RecordNovelProof(novelProofFor(*f4), "3"); err != nil {
		t.Fatal(err)
	}
	confirm, err := json.Marshal(novelConfirmPayload{FindingID: "f4", ProofHash: confirmDigestB, SourceBinding: "sha256:" + strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	craftConfirm(t, &cr.Log, Event{Entity: EntityFinding, ID: "f4", Kind: EventNovelConfirm,
		PreviousState: string(FindingNovelCandidate), NewState: string(FindingConfirmedNovel),
		TS: "4", Adjudicator: NovelConfirmAdjudicator, Payload: confirm})
	want := map[string]ReproOutcome{"f1": Reproduced, "f2": NotApplicable}
	if got := ReproFromEvents(*cr); !reflect.DeepEqual(got, want) {
		t.Fatalf("ReproFromEvents() = %v, want proof-derived %v (historical and reference-tampered novelty stays NotRun)", got, want)
	}
}

// A proof-promoted novel run round-trips through SaveRun/LoadRun with event-derived
// outcomes, and an altered novel outcome in the stored record fails reconstruction.
func TestNovelProofRunRoundtripKeepsReconstructionConsistent(t *testing.T) {
	cr := novelFindingsCaseRun(t, "f1", "f2", "f3")
	promoteNovelFixtures(t, cr)
	if err := cr.Close("5"); err != nil {
		t.Fatalf("close promoted-novel case: %v", err)
	}
	wantRepro := map[string]ReproOutcome{"f1": Reproduced, "f2": NotApplicable}
	if got := ReproFromEvents(*cr); !reflect.DeepEqual(got, wantRepro) {
		t.Fatalf("ReproFromEvents() = %v, want %v", got, wantRepro)
	}
	wantData := RunData{ID: "run-1", Cases: []CaseResult{{
		Case: "case", Issues: append([]Issue(nil), cr.Issues...),
		IssueStates:      map[string]IssueState{"D1": IssueFN},
		Primary:          map[string]string{"D1": ""},
		FindingStates:    map[string]FindingState{"f1": FindingConfirmedNovel, "f2": FindingConfirmedNovel, "f3": FindingConfirmedNovel},
		ReportedSeverity: map[string]Severity{"f1": High, "f2": High, "f3": High},
		Repro:            wantRepro,
	}}}
	stored := StoredRun{
		K: 1, WeightedRecallW: 0.5,
		Record:        RunRecord{State: RunCompleted, ManifestSHA256: "manifest-digest", Data: wantData},
		CaseRuns:      map[string]CaseRun{"case": *cr},
		CaseOutcomes:  map[string]string{"case": ""},
		CaseControls:  map[string]bool{"case": false},
		CaseResources: map[string]CaseRunResources{"case": {}},
	}
	runDir := filepath.Join(t.TempDir(), "run-1")
	if err := SaveRun(runDir, stored); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRun(runDir)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ManifestSHA256: "manifest-digest", MetricConfig: MetricConfig{WeightedRecallW: 0.5}, Cases: []ManifestCase{{ID: "case"}}}
	data, err := ReconstructCompletedRun(loaded, manifest)
	if err != nil {
		t.Fatalf("promoted-novel run must reconstruct from persisted events: %v", err)
	}
	if !reflect.DeepEqual(data.Cases[0].Repro, wantRepro) {
		t.Fatalf("reconstructed Repro = %v, want %v", data.Cases[0].Repro, wantRepro)
	}
	loaded.Record.Data.Cases[0].Repro["f1"] = NotReproduced
	if _, err := ReconstructCompletedRun(loaded, manifest); err == nil {
		t.Fatal("reconstruction accepted an altered novel outcome detached from its proof events")
	}
}

// A stored resource must tell an unknown token usage (absent or null) from a measured zero across save and load.
func TestSaveLoadRunKeepsUnknownTokensDistinctFromMeasuredZero(t *testing.T) {
	measuredZero := 0
	stored := StoredRun{
		K: 1, Record: RunRecord{State: RunAdjudicating},
		CaseResources: map[string]CaseRunResources{
			"c1": {CostUSD: 0.1, AgentSeconds: 2},
			"c2": {CostUSD: 0.1, AgentSeconds: 2, Tokens: &measuredZero},
		},
	}
	runDir := filepath.Join(t.TempDir(), "run-1")
	if err := SaveRun(runDir, stored); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		CaseResources map[string]map[string]json.RawMessage `json:"case_resources"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw.CaseResources["c1"]["tokens"]; present {
		t.Fatalf("unknown token usage must stay absent in run.json, got %s", raw.CaseResources["c1"])
	}
	if got := string(raw.CaseResources["c2"]["tokens"]); got != "0" {
		t.Fatalf("measured zero tokens = %s, want 0", got)
	}
	loaded, err := LoadRun(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if tokens := loaded.CaseResources["c1"].Tokens; tokens != nil {
		t.Fatalf("reloaded unknown tokens = %d, want nil", *tokens)
	}
	if tokens := loaded.CaseResources["c2"].Tokens; tokens == nil || *tokens != 0 {
		t.Fatalf("reloaded measured zero tokens = %v, want pointer to 0", tokens)
	}
	var explicitNull CaseRunResources
	if err := json.Unmarshal([]byte(`{"cost_usd":0.1,"agent_seconds":2,"tokens":null}`), &explicitNull); err != nil {
		t.Fatal(err)
	}
	if tokens := explicitNull.Tokens; tokens != nil {
		t.Fatalf("null tokens decoded to %d, want nil", *tokens)
	}
}
