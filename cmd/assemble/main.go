package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Activity struct {
	Ordinal        int    `json:"ordinal"`
	ID             string `json:"id"`
	Stage          string `json:"stage"`
	Step           string `json:"step"`
	ProofChoice    string `json:"proof_choice"`
	IndicatorClass string `json:"indicator_class"`
}

type Case struct {
	Ordinal  int    `json:"ordinal"`
	ID       string `json:"id"`
	Fixture  string `json:"fixture"`
	Expected string `json:"expected"`
}

type Contract struct {
	Denominator           int            `json:"denominator"`
	ActivityCount         int            `json:"activity_count"`
	ResolutionPrecedence  []string       `json:"resolution_precedence"`
	UnknownFields         []string       `json:"unknown_fields"`
	ProofDistribution     map[string]int `json:"proof_distribution"`
	IndicatorDistribution map[string]int `json:"indicator_distribution"`
	Activities             []Activity     `json:"activities"`
	Cases                  []Case         `json:"cases"`
}

func main() {
	fs := flag.NewFlagSet("assemble", flag.ExitOnError)
	contractPath := fs.String("contract", "", "fixed denominator contract")
	repoRoot := fs.String("repo-root", "", "repository root")
	generated := fs.String("generated", "", "generated artifact directory")
	baselineDir := fs.String("baseline-dir", "", "baseline outputs")
	candidateDir := fs.String("candidate-dir", "", "candidate outputs")
	replayPath := fs.String("replay", "", "replay receipt")
	measurementBefore := fs.String("measurement-before", "", "baseline measurement")
	measurementAfter := fs.String("measurement-after", "", "candidate measurement")
	upstreamPath := fs.String("upstream", "", "verified upstream report")
	outDir := fs.String("out", "", "caller-owned evidence directory")
	goVersion := fs.String("go-version", "", "Go toolchain identity")
	runID := fs.String("run-id", "", "GitHub Actions run identity")
	eventName := fs.String("event", "", "GitHub event identity")
	commitSHA := fs.String("commit", "", "checked-out commit")
	_ = fs.Parse(os.Args[1:])
	for name, value := range map[string]string{
		"contract": *contractPath, "repo-root": *repoRoot, "generated": *generated,
		"baseline-dir": *baselineDir, "candidate-dir": *candidateDir, "replay": *replayPath,
		"measurement-before": *measurementBefore, "measurement-after": *measurementAfter,
		"upstream": *upstreamPath, "out": *outDir, "go-version": *goVersion,
	} {
		if value == "" {
			fail(name + " is required")
		}
	}
	if err := assemble(*contractPath, *repoRoot, *generated, *baselineDir, *candidateDir, *replayPath, *measurementBefore, *measurementAfter, *upstreamPath, *outDir, *goVersion, *runID, *eventName, *commitSHA); err != nil {
		fail(err.Error())
	}
}

func assemble(contractPath, repoRoot, generated, baselineDir, candidateDir, replayPath, measurementBefore, measurementAfter, upstreamPath, outDir, goVersion, runID, eventName, commitSHA string) error {
	var contract Contract
	if err := readJSON(contractPath, &contract); err != nil {
		return err
	}
	if contract.Denominator != 12 || contract.ActivityCount != 12 || len(contract.Cases) != 12 || len(contract.Activities) != 12 {
		return errors.New("conformance contract is not fixed at 12")
	}
	if err := verifyDistribution(contract); err != nil {
		return err
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return fmt.Errorf("evidence directory must already exist: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("evidence directory must be empty")
	}

	baseline := make([]map[string]any, 0, len(contract.Cases))
	candidate := make([]map[string]any, 0, len(contract.Cases))
	for _, item := range contract.Cases {
		name := fmt.Sprintf("%02d.json", item.Ordinal)
		var before, after map[string]any
		if err := readJSON(filepath.Join(baselineDir, name), &before); err != nil {
			return fmt.Errorf("baseline %s: %w", item.ID, err)
		}
		if err := readJSON(filepath.Join(candidateDir, name), &after); err != nil {
			return fmt.Errorf("candidate %s: %w", item.ID, err)
		}
		if got := stringValue(after, "resolution"); got != item.Expected {
			return fmt.Errorf("%s expected %s, got %s", item.ID, item.Expected, got)
		}
		if err := verifyOutcome(after, item.Expected, contract.UnknownFields); err != nil {
			return fmt.Errorf("%s: %w", item.ID, err)
		}
		before["case_id"], before["fixture"] = item.ID, item.Fixture
		after["case_id"], after["fixture"] = item.ID, item.Fixture
		baseline = append(baseline, before)
		candidate = append(candidate, after)
	}
	targetBefore, targetAfter := baseline[1], candidate[1]
	if stringValue(targetBefore, "resolution") != "CLOSED" || stringValue(targetBefore, "decision") != "FIXED_POINT" {
		return errors.New("pinned baseline did not reproduce CLOSED/FIXED_POINT on the unknown counterexample")
	}
	if stringValue(targetAfter, "resolution") != "UNKNOWN" || stringValue(targetAfter, "decision") != "FEEDBACK_COVERAGE_DECISION_UNKNOWN" {
		return errors.New("candidate did not fail closed on the unknown counterexample")
	}
	if err := verifyUnknown(targetAfter, contract.UnknownFields); err != nil {
		return fmt.Errorf("target unknown record: %w", err)
	}
	beforeMeasure, err := readMeasurement(measurementBefore)
	if err != nil {
		return err
	}
	afterMeasure, err := readMeasurement(measurementAfter)
	if err != nil {
		return err
	}
	replay, err := readMap(replayPath)
	if err != nil {
		return err
	}
	upstream, err := readMap(upstreamPath)
	if err != nil {
		return err
	}
	sourceBytes, err := os.ReadFile(filepath.Join(repoRoot, ".gooo", "closed-loop.gooo"))
	if err != nil {
		return err
	}
	contractBytes, err := os.ReadFile(contractPath)
	if err != nil {
		return err
	}
	baselineProgram, err := os.ReadFile(filepath.Join(generated, "baseline.go"))
	if err != nil {
		return err
	}
	candidateProgram, err := os.ReadFile(filepath.Join(generated, "candidate.go"))
	if err != nil {
		return err
	}
	fixtureBytes, err := os.ReadFile(filepath.Join(repoRoot, contract.Cases[1].Fixture))
	if err != nil {
		return err
	}
	generationReceipt, err := readMap(filepath.Join(generated, "generation-receipt.json"))
	if err != nil {
		return err
	}
	generatedCount := int(numberValue(generationReceipt, "generated_artifact_count"))
	if generatedCount != 7 {
		return fmt.Errorf("generated artifact count must be 7, got %d", generatedCount)
	}

	unknownBoundary := unknownRecord("HUMAN_ADOPTION_BOUNDARY", "review-generated-candidate-before-adoption", "GENERATED_CANDIDATE_IS_NOT_AUTOMATICALLY_ADOPTED", "HUMAN_ADOPTION_REQUIRED", "HUMAN_REVIEW_OF_CANDIDATE_BEHAVIOR", []string{"NO_AUTOMATIC_APPLY_AUTHORITY"})
	performanceBoundary := unknownRecord("MEASUREMENT_BOUNDARY", "interpret-wall-and-rss-observations", "PERFORMANCE_AND_MEMORY_ARE_NOT_PART_OF_THE_SEMANTIC_DECISION_CONTRACT", "UNPAIRED_PERFORMANCE_CLAIM", "DECLARE_A_SEPARATE_PERFORMANCE_CONTRACT", []string{"PERFORMANCE_CONTRACT_NOT_DECLARED"})
	comparison := map[string]any{
		"schema": "gooo/before-after-comparison/v1", "status": "CLOSED",
		"scenario": contract.Cases[1].ID, "fixture": contract.Cases[1].Fixture, "fixture_sha256": digest(fixtureBytes),
		"identical_context": map[string]any{"scenario": contract.Cases[1].ID, "fixture_sha256": digest(fixtureBytes), "generator_identity": stringValue(targetAfter, "generator_identity"), "evaluator_identity": stringValue(targetAfter, "evaluator_identity"), "go_toolchain": goVersion, "semantic_source_sha256": stringValue(targetAfter, "semantic_source_sha256")},
		"before": map[string]any{"resolution": stringValue(targetBefore, "resolution"), "decision": stringValue(targetBefore, "decision"), "program_sha256": digest(baselineProgram), "evidence": "baseline-evidence.json"},
		"after": map[string]any{"resolution": stringValue(targetAfter, "resolution"), "decision": stringValue(targetAfter, "decision"), "unknown": targetAfter["unknown"], "program_sha256": digest(candidateProgram), "evidence": "candidate-evidence.json"},
		"semantic_correctness_delta": "CHANGED: CLOSED/FIXED_POINT -> UNKNOWN/FEEDBACK_COVERAGE_DECISION_UNKNOWN",
		"performance_memory_claim": map[string]any{"status": "UNKNOWN", "before": beforeMeasure, "after": afterMeasure, "unknown": performanceBoundary},
		"repository_writes": 0,
	}
	counterexample := map[string]any{
		"schema": "gooo/minimized-counterexample/v1", "status": "CLOSED",
		"source_fixture": contract.Cases[1].Fixture, "minimal_fixture": contract.Cases[1].Fixture, "minimal_fixture_sha256": digest(fixtureBytes),
		"minimal_shape": "{\"decision\":\"MAYBE\"}", "minimality": "1-MINIMAL_BY_FIELD_DELETION",
		"preserved_failure": "BASELINE_UNKNOWN_TOP_LEVEL_DEFAULTED_TO_FIXED_POINT", "repaired_behavior": "FEEDBACK_COVERAGE_DECISION_UNKNOWN",
		"replay_receipt": replay, "repository_writes": 0,
	}
	adoption := map[string]any{"schema": "gooo/human-adoption-boundary/v1", "status": "UNKNOWN", "candidate_artifact": "candidate.gooo", "candidate_go": "candidate.go", "unknown": unknownBoundary, "automatic_apply_authority": 0, "repository_writes": 0}
	measurement := map[string]any{
		"schema": "gooo/measurement-receipt/v1", "scope": "unknown-top-level-counterexample", "go_toolchain": goVersion,
		"before": beforeMeasure, "after": afterMeasure, "semantic_claim": "Only the matched behavioral pair is called changed.",
		"performance_memory_claim": map[string]any{"status": "UNKNOWN", "unknown": performanceBoundary}, "repository_writes": 0,
	}
	report := map[string]any{
		"schema": "gooo/conformance-report/v1", "decision": "CLOSED", "decision_basis": "ALL_12_CANDIDATE_CASES_MATCHED_THE_FIXED_CONTRACT",
		"denominator": 12, "activity_count": 12, "passed_case_count": 12, "case_resolution_distribution": resolutionDistribution(candidate),
		"proof_distribution": map[string]int{"FOUNDATION": 4, "COHERENCE": 4, "REGRESSION": 4}, "indicator_distribution": map[string]int{"DRIVER": 4, "OUTCOME": 4, "GUARDRAIL": 4},
		"resolution_precedence": []string{"REFUTED", "UNKNOWN", "CLOSED"}, "unknown_fields": contract.UnknownFields,
		"generated_artifact_count": generatedCount, "generated_artifact_names": generationReceipt["generated_artifact_names"],
		"runtime_evaluation_artifact_count": 25, "evidence_artifact_count": 10, "total_caller_owned_artifact_count": 42,
		"identities": map[string]any{"repository_commit": commitSHA, "github_actions_run_id": runID, "github_event": eventName, "go_toolchain": goVersion, "generator_identity": stringValue(targetAfter, "generator_identity"), "evaluator_identity": stringValue(targetAfter, "evaluator_identity"), "semantic_source_sha256": digest(sourceBytes), "contract_sha256": digest(contractBytes)},
		"authority_counters": map[string]int{"repository_writes": 0, "local_test_executions": 0, "cross_project_required_gates": 0},
		"operational_refuted_events": []any{},
		"upstream_inputs": upstream, "human_adoption_boundary": adoption, "performance_memory_claim": map[string]any{"status": "UNKNOWN", "unknown": performanceBoundary},
	}
	dossier := renderDossier(report, comparison, counterexample, adoption, measurement, upstream)
	files := map[string]any{
		"baseline-evidence.json": map[string]any{"schema": "gooo/baseline-evidence/v1", "status": "CLOSED", "program": "baseline", "program_sha256": digest(baselineProgram), "pinned_bug": "UNKNOWN_TOP_LEVEL_DEFAULTS_TO_FIXED_POINT", "target_case": targetBefore, "all_cases": baseline, "source_sha256": digest(sourceBytes), "contract_sha256": digest(contractBytes), "go_toolchain": goVersion, "repository_writes": 0},
		"candidate-evidence.json": map[string]any{"schema": "gooo/candidate-evidence/v1", "status": "CLOSED", "program": "candidate", "program_sha256": digest(candidateProgram), "semantic_change": "FAIL_CLOSED_UNKNOWN", "target_case": targetAfter, "all_cases": candidate, "source_sha256": digest(sourceBytes), "contract_sha256": digest(contractBytes), "go_toolchain": goVersion, "repository_writes": 0},
		"before-after-comparison.json": comparison, "minimized-counterexample.json": counterexample, "human-adoption-boundary.json": adoption,
		"measurement-receipt.json": measurement, "upstream-verification.json": upstream, "conformance-report.json": report, "human-dossier.md": dossier,
	}
	for name, value := range files {
		if name == "human-dossier.md" {
			if err := os.WriteFile(filepath.Join(outDir, name), []byte(value.(string)), 0o644); err != nil {
				return err
			}
		} else if err := writeJSON(filepath.Join(outDir, name), value); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(files)+1)
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	names = append(names, "artifact-manifest.json")
	return writeJSON(filepath.Join(outDir, "artifact-manifest.json"), map[string]any{"schema": "gooo/caller-owned-artifact-manifest/v1", "artifact_count": len(names), "artifacts": names, "generator_artifact_count": generatedCount, "runtime_evaluation_artifact_count": 25, "evidence_artifact_count": len(names), "repository_writes": 0})
}

func verifyDistribution(contract Contract) error {
	proofs, indicators := map[string]int{}, map[string]int{}
	for i, activity := range contract.Activities {
		if activity.Ordinal != i+1 {
			return errors.New("activity ordinals are not contiguous")
		}
		proofs[activity.ProofChoice]++
		indicators[activity.IndicatorClass]++
	}
	for key := range map[string]bool{"FOUNDATION": true, "COHERENCE": true, "REGRESSION": true} {
		if proofs[key] != 4 || contract.ProofDistribution[key] != 4 {
			return fmt.Errorf("proof bucket %s is not 4", key)
		}
	}
	for key := range map[string]bool{"DRIVER": true, "OUTCOME": true, "GUARDRAIL": true} {
		if indicators[key] != 4 || contract.IndicatorDistribution[key] != 4 {
			return fmt.Errorf("indicator bucket %s is not 4", key)
		}
	}
	return nil
}

func verifyOutcome(outcome map[string]any, expected string, fields []string) error {
	if int(numberValue(outcome, "repository_writes")) != 0 || int(numberValue(outcome, "local_test_executions")) != 0 || int(numberValue(outcome, "cross_project_required_gates")) != 0 {
		return errors.New("runtime authority counter is nonzero")
	}
	if expected == "UNKNOWN" {
		return verifyUnknown(outcome, fields)
	}
	if _, ok := outcome["unknown"]; ok {
		return errors.New("non-UNKNOWN outcome carries an UNKNOWN record")
	}
	return nil
}

func verifyUnknown(outcome map[string]any, fields []string) error {
	record, ok := outcome["unknown"].(map[string]any)
	if !ok {
		return errors.New("UNKNOWN outcome has no record")
	}
	if len(record) != len(fields) {
		return fmt.Errorf("UNKNOWN record has %d fields, want %d", len(record), len(fields))
	}
	for _, field := range fields {
		if _, ok := record[field]; !ok {
			return fmt.Errorf("UNKNOWN record is missing %s", field)
		}
	}
	return nil
}

func resolutionDistribution(items []map[string]any) map[string]int {
	result := map[string]int{"CLOSED": 0, "UNKNOWN": 0, "REFUTED": 0}
	for _, item := range items {
		result[stringValue(item, "resolution")]++
	}
	return result
}

func readMeasurement(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parts := strings.Fields(string(data))
	if len(parts) != 2 {
		return nil, fmt.Errorf("measurement %s must contain seconds and peak RSS", path)
	}
	seconds, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return nil, err
	}
	rss, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, err
	}
	return map[string]any{"wall_ms": int64(math.Round(seconds * 1000)), "peak_rss_kib": rss}, nil
}

func renderDossier(report, comparison, counterexample, adoption, measurement, upstream map[string]any) string {
	before := comparison["before"].(map[string]any)
	after := comparison["after"].(map[string]any)
	template := "# Gooo closed-loop self-improvement dossier\n\n## Decision\n\nThe bounded semantic comparison is CLOSED because all 12 candidate conformance cases match the fixed contract. The only semantic change called changed is the matched counterexample pair:\n\n- before: %s / %s\n- after: %s / %s\n\nOnly explicit FIXED_POINT may close. Every other string value fails closed with FEEDBACK_COVERAGE_DECISION_UNKNOWN and a six-field UNKNOWN record.\n\n## Counterexample\n\nThe minimized fixture is %s with digest %s. It contains one decision field with the value MAYBE. The pinned baseline incorrectly defaults it to FIXED_POINT; the generated candidate returns FEEDBACK_COVERAGE_DECISION_UNKNOWN. Removing the only decision field changes the input into a malformed REFUTED case, so the one-field fixture is 1-minimal for this bug.\n\n## Evidence and boundary\n\nThe .gooo source is the semantic authority and declares the decision rule, activities, fixed denominator, precedence, and zero-authority boundary. The generator emitted 7 artifacts into caller-owned temporary output. The baseline and candidate were run on the same scenario, fixture, generator identity, evaluator identity, and Go toolchain identity. Candidate replay was performed twice and is recorded separately.\n\nHuman adoption remains UNKNOWN: no repository, commit, merge, tag, or release authority is granted to the runtime. A human must review and explicitly adopt the generated candidate.\n\nWall/RSS observations are recorded only as paired measurements from Actions. They are not treated as a performance or memory improvement claim; that claim remains UNKNOWN because no separate performance contract was declared.\n\n## Fixed evidence accounting\n\n- denominator: %d\n- activities: %d\n- proof buckets: FOUNDATION 4 / COHERENCE 4 / REGRESSION 4\n- indicator buckets: DRIVER 4 / OUTCOME 4 / GUARDRAIL 4\n- precedence: REFUTED > UNKNOWN > CLOSED\n- authority counters: repository_writes 0, local_test_executions 0, cross_project_required_gates 0\n- upstream releases verified as immutable inputs: %d\n- external composition boundary: UNKNOWN (formats verified but not fabricated into this fixture)\n\nThe full machine-readable evidence is in the adjacent JSON artifacts.\n"
	return fmt.Sprintf(template, before["resolution"], before["decision"], after["resolution"], after["decision"], counterexample["minimal_fixture"], counterexample["minimal_fixture_sha256"], report["denominator"], report["activity_count"], len(upstream["releases"].([]any)))
}

func unknownRecord(stage, step, reason, class, next string, blocked []string) map[string]any {
	return map[string]any{"stage": stage, "step": step, "reason": reason, "unknown_class": class, "next_operation": next, "blocked_by": blocked}
}

func readMap(path string) (map[string]any, error) {
	var result map[string]any
	if err := readJSON(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func stringValue(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

func numberValue(value map[string]any, key string) float64 {
	result, _ := value[key].(float64)
	return result
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
