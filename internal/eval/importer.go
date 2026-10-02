package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alesierraalta/tpp/internal/bench"
)

// Imported is the persisted evaluation run assembled from runner artifacts.
type Imported struct {
	Run StoredRun
}

type importedAggregate struct {
	Model           string           `json:"model"`
	BudgetExhausted bool             `json:"budget_exhausted"`
	Provenance      bench.Provenance `json:"provenance"`
}

type importedResult struct {
	bench.Result
}

// ImportRun converts one replicate's runner results into adjudicable case runs.
func ImportRun(resultsDir, benchDir string, manifest Manifest, policy Policy, harnessLabel string, replicate int, ts string) (Imported, error) {
	if replicate < 1 {
		return Imported{}, fmt.Errorf("replicate must be positive, got %d", replicate)
	}
	if harnessLabel == "" {
		return Imported{}, fmt.Errorf("harness label is empty")
	}
	if mismatches := VerifyCases(benchDir, manifest); len(mismatches) > 0 {
		return Imported{}, fmt.Errorf("benchmark cases do not match manifest: %s", strings.Join(mismatches, ", "))
	}
	aggregateData, err := os.ReadFile(filepath.Join(resultsDir, "aggregate.json"))
	if err != nil {
		return Imported{}, fmt.Errorf("read aggregate.json: %w", err)
	}
	var aggregate importedAggregate
	if err := json.Unmarshal(aggregateData, &aggregate); err != nil {
		return Imported{}, fmt.Errorf("parse aggregate.json: %w", err)
	}
	// A reading sealed under one manifest may not be imported under another: the relabel would
	// fabricate comparability with runs the supplied manifest never measured.
	if bound := aggregate.Provenance.ManifestSHA256; bound != "" && bound != manifest.ManifestSHA256 {
		return Imported{}, fmt.Errorf("aggregate provenance is bound to manifest %s, not the supplied %s: refusing to relabel another manifest's reading", bound, manifest.ManifestSHA256)
	}
	if aggregate.Model == "" {
		aggregate.Model = aggregate.Provenance.Model
	}
	suiteAborted := suiteBudgetAbort(aggregate)
	components := HarnessComponents{
		SkillsDigest:    aggregate.Provenance.SkillsDigest,
		Runner:          aggregate.Provenance.Runner,
		AgentConfigMode: aggregate.Provenance.AgentConfig,
	}
	harnessID := HarnessID(components)
	if harnessID == "" {
		return Imported{}, fmt.Errorf("compute harness id")
	}
	if err := AppendHarness(filepath.Join(benchDir, "harnesses.jsonl"), HarnessRecord{
		Label: harnessLabel, ID: harnessID, Components: components, Registered: ts,
	}); err != nil {
		return Imported{}, err
	}

	stored := StoredRun{
		K: replicate, HarnessLabel: harnessLabel, SourceResultsDir: resultsDir,
		Record: RunRecord{
			Data: RunData{ID: fmt.Sprintf("run-%d", replicate)}, State: RunAdjudicating,
			ManifestSHA256: manifest.ManifestSHA256, PolicySHA256: policy.PolicySHA256,
			HarnessID: harnessID, Model: aggregate.Model, Runner: aggregate.Provenance.Runner,
			Environment: aggregate.Provenance.Environment,
		},
		WeightedRecallW: manifest.MetricConfig.WeightedRecallW,
		CaseRuns:        make(map[string]CaseRun, len(manifest.Cases)),
		CaseOutcomes:    make(map[string]string, len(manifest.Cases)),
		CaseControls:    make(map[string]bool, len(manifest.Cases)),
		CaseResources:   make(map[string]CaseRunResources, len(manifest.Cases)),
	}
	for _, manifestCase := range manifest.Cases {
		caseID := manifestCase.ID
		key, err := bench.LoadKey(filepath.Join(benchDir, "cases", caseID))
		if err != nil {
			return Imported{}, fmt.Errorf("load case %s key: %w", caseID, err)
		}
		issues, err := IssuesFromKey(key)
		if err != nil {
			return Imported{}, err
		}
		caseDir := filepath.Join(resultsDir, caseID, fmt.Sprint(replicate))
		resultData, err := os.ReadFile(filepath.Join(caseDir, "result.json"))
		if err != nil && suiteAborted && os.IsNotExist(err) {
			// The suite budget stopped the run before this case started: it never executed, so its
			// ledger records the abort with no findings and no usage rather than a measured zero.
			cr := CaseRun{Case: caseID, Issues: issues}
			if err := appendCaseTransition(&cr, CaseRunCreated, CaseRunRunning, ts, ""); err != nil {
				return Imported{}, err
			}
			if err := appendCaseTransition(&cr, CaseRunRunning, CaseRunAborted, ts, "suite budget exhausted before this case ran"); err != nil {
				return Imported{}, err
			}
			stored.CaseControls[caseID] = key.IsCleanControl()
			stored.CaseOutcomes[caseID] = "missing_execution"
			stored.CaseRuns[caseID] = cr
			continue
		}
		if err != nil {
			return Imported{}, fmt.Errorf("read result for case %s: %w", caseID, err)
		}
		var result importedResult
		if err := json.Unmarshal(resultData, &result); err != nil {
			return Imported{}, fmt.Errorf("parse result for case %s: %w", caseID, err)
		}
		// Unknown token usage in a manifest-bound reading must never be recorded as a measured
		// zero: bench.Result keeps it as a nil pointer, and a bound reading without it is refused.
		if aggregate.Provenance.ManifestSHA256 != "" && !result.Invalid && result.Tokens == nil {
			return Imported{}, fmt.Errorf("case %s: token usage is missing in a manifest-bound reading; refusing to record an unknown usage as measured zero", caseID)
		}

		planBytes, planErr := os.ReadFile(filepath.Join(caseDir, "test-plan.md"))
		planPresent := planErr == nil
		if planErr != nil && !os.IsNotExist(planErr) {
			return Imported{}, fmt.Errorf("read plan for case %s: %w", caseID, planErr)
		}
		plan := string(planBytes)
		rowFacts := bench.FindingFacts(plan, key)
		cr := CaseRun{Case: caseID, Issues: issues}
		invalidReason := ""
		if result.Invalid {
			invalidReason = result.InvalidReason
			if invalidReason == "" {
				invalidReason = "runner marked case invalid"
			}
		}
		// A plan the runner found but the results no longer keep cannot be adjudicated (spec 15.6). A prose plan or
		// an unlocated row is the harness's own output failure: it is scored, never excluded (spec 4.4, 5.3).
		if invalidReason == "" && result.PlanFound && !planPresent {
			invalidReason = "kept plan is missing, so its findings cannot be adjudicated"
		}
		for _, row := range rowFacts {
			finding := Finding{
				ID: fmt.Sprintf("%s-f%d", caseID, row.Row), Row: row.Row,
				Fingerprint: row.Fingerprint, Location: row.Location, Description: row.Text,
				ReportedSeverityText: row.ReportedSeverity,
				Computed:             make(map[string]ComputedFacts, len(issues)),
			}
			if invalidReason != "" {
				finding.Invalid, finding.InvalidReason = true, invalidReason
			} else if !row.HasLocation {
				finding.Invalid, finding.InvalidReason = true, "finding has no location"
			}
			for _, issue := range issues {
				located := containsIssue(row.LocatedDefects, issue.ID)
				c5 := FactNA
				if issue.Reproduction.Applies {
					c5 = FactFalse
					if result.Catch.Caught[issue.ID] {
						c5 = FactTrue
					}
				}
				c2 := FactFalse
				if located {
					c2 = FactTrue
				}
				c4 := FactFalse
				if row.CitesLedger {
					c4 = FactTrue
				}
				finding.Computed[issue.ID] = ComputedFacts{C2: c2, C4Cited: c4, C5: c5}
			}
			cr.Findings = append(cr.Findings, finding)
		}

		control := key.IsCleanControl()
		stored.CaseControls[caseID] = control
		stored.CaseResources[caseID] = CaseRunResources{CostUSD: result.CostUSD, AgentSeconds: result.Seconds, Tokens: result.Tokens}
		if invalidReason != "" {
			stored.CaseOutcomes[caseID] = invalidReason
			stored.Record.State = RunInvalid
			if len(cr.Findings) == 0 {
				if err := appendCaseTransition(&cr, CaseRunCreated, CaseRunRunning, ts, ""); err != nil {
					return Imported{}, err
				}
				if err := appendCaseTransition(&cr, CaseRunRunning, CaseRunInvalid, ts, invalidReason); err != nil {
					return Imported{}, err
				}
			} else if err := cr.Admit(ts); err != nil {
				return Imported{}, fmt.Errorf("admit invalid case %s: %w", caseID, err)
			}
			if stored.Record.AbortReason == "" {
				stored.Record.AbortReason = fmt.Sprintf("case %s: %s", caseID, invalidReason)
			}
		} else if result.BudgetExhausted || result.Outcome == "budget_exhausted" {
			// The harness ran out of its own case budget (spec 5.3): the plan as it stands is
			// scored, Issues not found are FN, and it is never an infrastructure failure.
			stored.CaseOutcomes[caseID] = "budget_exhausted"
			if err := cr.Admit(ts); err != nil {
				return Imported{}, fmt.Errorf("admit budget-exhausted case %s: %w", caseID, err)
			}
		} else if result.Failed {
			if isInfrastructureFailure(result.FailReason) {
				stored.CaseOutcomes[caseID] = result.FailReason
				if stored.Record.State != RunInvalid {
					stored.Record.State = RunFailedInfrastructure
				}
				if err := appendCaseTransition(&cr, CaseRunCreated, CaseRunRunning, ts, ""); err != nil {
					return Imported{}, err
				}
				if err := appendCaseTransition(&cr, CaseRunRunning, CaseRunFailedInfrastructure, ts, result.FailReason); err != nil {
					return Imported{}, err
				}
			} else {
				stored.CaseOutcomes[caseID] = "unclassified_failure"
				if err := cr.Admit(ts); err != nil {
					return Imported{}, fmt.Errorf("admit case %s: %w", caseID, err)
				}
			}
		} else {
			if !planPresent || !result.PlanFound {
				stored.CaseOutcomes[caseID] = "no_plan"
			} else if result.PlanFormat == bench.FormatProse {
				stored.CaseOutcomes[caseID] = "prose_plan"
			} else {
				stored.CaseOutcomes[caseID] = "scored"
			}
			if err := cr.Admit(ts); err != nil {
				return Imported{}, fmt.Errorf("admit case %s: %w", caseID, err)
			}
		}
		if err := scanResultLeaks(caseDir, caseID, replicate, manifest.Canary, result.Result, &stored.Record.Leaks); err != nil {
			return Imported{}, err
		}
		stored.CaseRuns[caseID] = cr
	}
	// A suite whose own budget ran out never measured the whole reading, so the run is ABORTED on
	// budget (spec 5.4): that is the state the comparator reads as the hard completeness blocker.
	// A run already carrying a stronger terminal state keeps it, so an invalid instrument, a leak
	// or an infrastructure failure is never relabelled as a budget abort.
	if suiteAborted && stored.Record.State == RunAdjudicating {
		stored.Record.State = RunAborted
		stored.Record.AbortReason = "budget"
	}
	// Provenance that does not prove a valid, complete, budget-supported execution may never
	// become a COMPLETED record (spec 15): land it INVALID with the reason. Cases already
	// dispositioned failed or invalid set a terminal state of their own.
	if reason := provenanceCompromised(aggregate.Provenance); reason != "" && stored.Record.State == RunAdjudicating {
		stored.Record.State = RunInvalid
		stored.Record.AbortReason = reason
	}
	return Imported{Run: stored}, nil
}

// suiteBudgetAbort reports whether the harness's own suite budget stopped a manifest-bound
// reading: the aggregate says a budget ran out, the runner proves a valid instrument and a
// supported budget, and the execution never completed. A case that ran out of its own budget
// leaves the execution complete and is scored (spec 5.3), so it never lands here; an invalid
// instrument or an unverified budget keeps the stronger invalid handling (spec 15).
func suiteBudgetAbort(aggregate importedAggregate) bool {
	p := aggregate.Provenance
	if !aggregate.BudgetExhausted || p.ManifestSHA256 == "" {
		return false
	}
	if p.InstrumentValid == nil || !*p.InstrumentValid || p.BudgetStatus != "supported" {
		return false
	}
	return p.ExecutionComplete != nil && !*p.ExecutionComplete
}

// provenanceCompromised names why a manifest-bound reading may not become a COMPLETED record:
// an invalid instrument, an unverified budget, or an incomplete execution — the same three
// refusals the comparator applies to the readings it is offered.
func provenanceCompromised(p bench.Provenance) string {
	if p.ManifestSHA256 == "" {
		return ""
	}
	switch {
	case p.InstrumentValid == nil || !*p.InstrumentValid:
		return "provenance instrument is invalid or lacks validity evidence"
	case p.BudgetStatus != "supported":
		return "provenance budget support is not verified: " + p.BudgetUnsupportedReason
	case p.ExecutionComplete == nil || !*p.ExecutionComplete:
		return "provenance execution is incomplete"
	}
	return ""
}

func appendCaseTransition(cr *CaseRun, from, to CaseRunState, ts, reason string) error {
	_, err := cr.Log.Append(Event{
		Entity: EntityCaseRun, ID: cr.Case, Kind: EventTransition,
		PreviousState: string(from), NewState: string(to), TS: ts, Reason: reason,
	})
	return err
}

func containsIssue(ids []string, issue string) bool {
	for _, id := range ids {
		if id == issue {
			return true
		}
	}
	return false
}

func hasInvalidFinding(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Invalid {
			return true
		}
	}
	return false
}

func isInfrastructureFailure(reason string) bool {
	lower := strings.ToLower(reason)
	for _, signal := range []string{"rate limit", "429", "5xx", "network", "timeout connecting"} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	for status := 500; status <= 599; status++ {
		if strings.Contains(lower, fmt.Sprint(status)) {
			return true
		}
	}
	return false
}

func scanResultLeaks(caseDir, caseID string, replicate int, canary string, result bench.Result, leaks *[]Leak) error {
	for _, name := range []string{"agent.log", "test-plan.md"} {
		path := filepath.Join(caseDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s for leak scan: %w", path, err)
		}
		source := filepath.ToSlash(filepath.Join(caseID, fmt.Sprint(replicate), name))
		found, err := ScanLeaks(source, strings.NewReader(string(data)), canary)
		if err != nil {
			return err
		}
		*leaks = append(*leaks, found...)
	}
	// The saved test snapshots are evidence the replay trusts later, so their content is scanned
	// like the plan and the agent log: a snapshot that leaks the canary or a key/fix path is a
	// ground-truth leak. Each snapshot is read through the safe loader, so bytes that no longer
	// hash to the digest result.json records fail the import instead of being scanned as trusted.
	artifactRoot := filepath.Join(caseDir, "test-artifacts")
	for _, artifact := range result.TestArtifacts {
		data, err := bench.ReadSavedArtifact(artifactRoot, artifact)
		if err != nil {
			return fmt.Errorf("saved test artifact for leak scan: %w", err)
		}
		source := filepath.ToSlash(filepath.Join(caseID, fmt.Sprint(replicate), "test-artifacts", filepath.FromSlash(artifact.Path)))
		found, err := ScanLeaks(source, strings.NewReader(string(data)), canary)
		if err != nil {
			return err
		}
		*leaks = append(*leaks, found...)
	}
	return nil
}
