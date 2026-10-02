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
	Model      string           `json:"model"`
	Provenance bench.Provenance `json:"provenance"`
}

type importedResult struct {
	bench.Result
	Tokens int `json:"tokens"`
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
	if aggregate.Model == "" {
		aggregate.Model = aggregate.Provenance.Model
	}
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
		if err != nil {
			return Imported{}, fmt.Errorf("read result for case %s: %w", caseID, err)
		}
		var result importedResult
		if err := json.Unmarshal(resultData, &result); err != nil {
			return Imported{}, fmt.Errorf("parse result for case %s: %w", caseID, err)
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
		if err := scanResultLeaks(caseDir, caseID, replicate, manifest.Canary, &stored.Record.Leaks); err != nil {
			return Imported{}, err
		}
		stored.CaseRuns[caseID] = cr
	}
	return Imported{Run: stored}, nil
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

func scanResultLeaks(caseDir, caseID string, replicate int, canary string, leaks *[]Leak) error {
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
	return nil
}
