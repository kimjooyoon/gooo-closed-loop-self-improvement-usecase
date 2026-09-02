package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	generatorIdentity = "gooo-loopgen@v0.1.0"
	evaluatorIdentity = "feedback-coverage-decision-evaluator@v0.1.0"
)

type Activity struct {
	Ordinal        int    `json:"ordinal"`
	ID             string `json:"id"`
	Stage          string `json:"stage"`
	Step           string `json:"step"`
	ProofChoice    string `json:"proof_choice"`
	IndicatorClass string `json:"indicator_class"`
	DependsOn      string `json:"depends_on,omitempty"`
}

type Case struct {
	Ordinal  int    `json:"ordinal"`
	ID       string `json:"id"`
	Fixture  string `json:"fixture"`
	Expected string `json:"expected"`
}

type Contract struct {
	Schema                string         `json:"schema"`
	ID                    string         `json:"id"`
	Fixed                 bool           `json:"fixed"`
	Denominator           int            `json:"denominator"`
	ActivityCount         int            `json:"activity_count"`
	ResolutionPrecedence  []string       `json:"resolution_precedence"`
	UnknownFields         []string       `json:"unknown_fields"`
	ProofDistribution     map[string]int `json:"proof_distribution"`
	IndicatorDistribution map[string]int `json:"indicator_distribution"`
	RuntimeContract       map[string]any `json:"runtime_contract"`
	Activities             []Activity     `json:"activities"`
	Cases                  []Case         `json:"cases"`
}

type Spec struct {
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	SemanticOwner  string            `json:"semantic_owner"`
	ClosedValue    string            `json:"closed_value"`
	UnknownPolicy  string            `json:"unknown_policy"`
	UnknownCode    string            `json:"unknown_code"`
	Precedence     []string          `json:"precedence"`
	UnknownFields  []string          `json:"unknown_fields"`
	Authority      map[string]string `json:"authority"`
	OutputBoundary string            `json:"output_boundary"`
	Generator      string            `json:"generator"`
	Evaluator      string            `json:"evaluator"`
	Toolchain      string            `json:"toolchain"`
	BaselineBug    string            `json:"baseline_bug"`
	Counterexample map[string]string `json:"counterexample"`
	Activities     []Activity        `json:"activities"`
}

type Artifact struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "generate" {
		fatal("usage: loopgen generate --source PATH --contract PATH --out ABSOLUTE_EMPTY_DIR --repo-root ABSOLUTE_REPO")
	}
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	sourcePath := fs.String("source", "", "authoritative .gooo source")
	contractPath := fs.String("contract", "", "fixed denominator contract")
	outPath := fs.String("out", "", "caller-owned output directory")
	repoRoot := fs.String("repo-root", "", "source repository root")
	_ = fs.Parse(os.Args[2:])
	if *sourcePath == "" || *contractPath == "" || *outPath == "" || *repoRoot == "" {
		fatal("source, contract, out, and repo-root are required")
	}
	if err := generate(*sourcePath, *contractPath, *outPath, *repoRoot); err != nil {
		fatal(err.Error())
	}
}

func generate(sourcePath, contractPath, outPath, repoRoot string) error {
	outPath, err := filepath.Abs(outPath)
	if err != nil {
		return err
	}
	repoRoot, err = filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(repoRoot, outPath)
	if err != nil {
		return err
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("runtime output must be outside the source repository")
	}
	entries, err := os.ReadDir(outPath)
	if err != nil {
		return fmt.Errorf("caller-owned output must already exist and be empty: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("caller-owned output directory is not empty")
	}

	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	contractBytes, err := os.ReadFile(contractPath)
	if err != nil {
		return err
	}
	var contract Contract
	if err := json.Unmarshal(contractBytes, &contract); err != nil {
		return fmt.Errorf("contract: %w", err)
	}
	spec, err := parseSource(string(sourceBytes))
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if err := validate(spec, contract); err != nil {
		return err
	}
	sourceDigest := digest(sourceBytes)
	contractDigest := digest(contractBytes)

	ir := map[string]any{
		"schema": "gooo/semantic-ir/feedback-coverage-decision/v1",
		"source": map[string]any{"path": ".gooo/closed-loop.gooo", "sha256": sourceDigest},
		"contract": map[string]any{"path": "contracts/denominator-v1.json", "sha256": contractDigest},
		"semantic_owner": spec.SemanticOwner,
		"decision_semantics": map[string]any{
			"closed_value": spec.ClosedValue,
			"unknown_policy": spec.UnknownPolicy,
			"unknown_code": spec.UnknownCode,
			"precedence": spec.Precedence,
			"unknown_fields": spec.UnknownFields,
		},
		"authority": spec.Authority,
		"output_boundary": spec.OutputBoundary,
		"toolchain": spec.Toolchain,
		"generator_identity": spec.Generator,
		"evaluator_identity": spec.Evaluator,
		"activities": spec.Activities,
	}
	if err := writeJSON(filepath.Join(outPath, "semantic-ir.json"), ir); err != nil {
		return err
	}

	baselineGooo := "gooo generated_feedback_coverage_baseline v1\n\n" +
		"decision closed_value=FIXED_POINT unknown_policy=DEFAULT_TO_FIXED_POINT unknown_code=BASELINE_BUG\n" +
		"baseline bug=UNKNOWN_TOP_LEVEL_DEFAULTS_TO_FIXED_POINT\n" +
		"authority repository_writes=0 output=caller_owned_temp_only\n"
	candidateGooo := "gooo generated_feedback_coverage_candidate v1\n\n" +
		"decision closed_value=FIXED_POINT unknown_policy=FAIL_CLOSED unknown_code=FEEDBACK_COVERAGE_DECISION_UNKNOWN\n" +
		"change from=UNKNOWN_TOP_LEVEL_DEFAULTS_TO_FIXED_POINT to=FAIL_CLOSED_UNKNOWN\n" +
		"authority repository_writes=0 output=caller_owned_temp_only\n"
	if err := os.WriteFile(filepath.Join(outPath, "baseline.gooo"), []byte(baselineGooo), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outPath, "candidate.gooo"), []byte(candidateGooo), 0o644); err != nil {
		return err
	}

	baselineGo := makeProgram("baseline", sourceDigest, true)
	candidateGo := makeProgram("candidate", sourceDigest, false)
	if err := os.WriteFile(filepath.Join(outPath, "baseline.go"), []byte(baselineGo), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outPath, "candidate.go"), []byte(candidateGo), 0o644); err != nil {
		return err
	}

	artifacts := make([]Artifact, 0, 5)
	for _, name := range []string{"semantic-ir.json", "baseline.gooo", "candidate.gooo", "baseline.go", "candidate.go"} {
		artifact, err := describe(filepath.Join(outPath, name), name)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact)
	}
	manifest := map[string]any{
		"schema": "gooo/generated-candidate-artifact/v1",
		"generator_identity": generatorIdentity,
		"evaluator_identity": evaluatorIdentity,
		"semantic_change": "UNKNOWN_TOP_LEVEL_DEFAULTS_TO_FIXED_POINT -> FAIL_CLOSED_UNKNOWN",
		"caller_owned_output": true,
		"repository_writes": 0,
		"artifacts": artifacts,
		"artifact_count": len(artifacts),
	}
	if err := writeJSON(filepath.Join(outPath, "candidate-artifact.json"), manifest); err != nil {
		return err
	}
	receipt := map[string]any{
		"schema": "gooo/generation-receipt/v1",
		"generator_identity": generatorIdentity,
		"evaluator_identity": evaluatorIdentity,
		"source_sha256": sourceDigest,
		"contract_sha256": contractDigest,
		"toolchain_declared": spec.Toolchain,
		"generated_artifact_count": 7,
		"generated_artifact_names": []string{"semantic-ir.json", "baseline.gooo", "candidate.gooo", "baseline.go", "candidate.go", "candidate-artifact.json", "generation-receipt.json"},
		"runtime_contract": map[string]int{"repository_writes": 0, "local_test_executions": 0, "cross_project_required_gates": 0},
	}
	return writeJSON(filepath.Join(outPath, "generation-receipt.json"), receipt)
}

func parseSource(source string) (Spec, error) {
	var spec Spec
	for lineNumber, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if lineNumber == 0 {
			if len(fields) < 3 || fields[0] != "gooo" {
				return spec, fmt.Errorf("line %d: first declaration must be gooo NAME VERSION", lineNumber+1)
			}
			spec.Name, spec.Version = fields[1], fields[2]
			continue
		}
		kv := parseKV(fields[1:])
		switch fields[0] {
		case "semantic_owner":
			spec.SemanticOwner = kv["owner"]
		case "decision":
			spec.ClosedValue, spec.UnknownPolicy, spec.UnknownCode = kv["closed_value"], kv["unknown_policy"], kv["unknown_code"]
		case "precedence":
			spec.Precedence = strings.Split(kv["order"], ">")
		case "unknown_fields":
			spec.UnknownFields = strings.Split(kv["fields"], ",")
		case "authority":
			spec.Authority = kv
		case "output":
			spec.OutputBoundary = kv["boundary"]
		case "toolchain":
			spec.Toolchain, spec.Generator, spec.Evaluator = kv["go"], kv["generator"], kv["evaluator"]
		case "baseline":
			spec.BaselineBug = kv["bug"]
		case "counterexample":
			spec.Counterexample = kv
		case "activity":
			ordinal, err := strconv.Atoi(kv["ordinal"])
			if err != nil {
				return spec, fmt.Errorf("line %d: activity ordinal: %w", lineNumber+1, err)
			}
			spec.Activities = append(spec.Activities, Activity{
				Ordinal: ordinal, ID: kv["id"], Stage: kv["stage"], Step: kv["step"],
				ProofChoice: kv["proof_choice"], IndicatorClass: kv["indicator_class"], DependsOn: kv["depends_on"],
			})
		}
	}
	return spec, nil
}

func parseKV(fields []string) map[string]string {
	result := make(map[string]string, len(fields))
	for _, field := range fields {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func validate(spec Spec, contract Contract) error {
	if !contract.Fixed || contract.Denominator != 12 || contract.ActivityCount != 12 || len(spec.Activities) != 12 {
		return errors.New("fixed denominator/activity contract is not exactly 12")
	}
	if strings.Join(spec.Precedence, ">") != "REFUTED>UNKNOWN>CLOSED" || strings.Join(contract.ResolutionPrecedence, ">") != "REFUTED>UNKNOWN>CLOSED" {
		return errors.New("resolution precedence is not REFUTED>UNKNOWN>CLOSED")
	}
	if strings.Join(spec.UnknownFields, ",") != "stage,step,reason,unknown_class,next_operation,blocked_by" || strings.Join(contract.UnknownFields, ",") != "stage,step,reason,unknown_class,next_operation,blocked_by" {
		return errors.New("unknown record does not have exactly six required fields")
	}
	if spec.ClosedValue != "FIXED_POINT" || spec.UnknownPolicy != "FAIL_CLOSED" || spec.UnknownCode != "FEEDBACK_COVERAGE_DECISION_UNKNOWN" {
		return errors.New("source does not declare the required fail-closed decision semantics")
	}
	if spec.SemanticOwner != "gooo" || spec.OutputBoundary != "caller_owned_temp_only" || spec.BaselineBug != "UNKNOWN_TOP_LEVEL_DEFAULTS_TO_FIXED_POINT" {
		return errors.New("source is missing semantic owner, output boundary, or pinned baseline declaration")
	}
	for key, want := range map[string]string{"repository_writes": "0", "local_test_executions": "0", "cross_project_required_gates": "0"} {
		if spec.Authority[key] != want || fmt.Sprint(contract.RuntimeContract[key]) != want {
			return fmt.Errorf("runtime contract %s is not zero", key)
		}
	}
	proofs := map[string]int{}
	indicators := map[string]int{}
	for i, activity := range spec.Activities {
		if activity.Ordinal != i+1 || activity.ID == "" || activity.Stage == "" || activity.Step == "" || activity.ProofChoice == "" || activity.IndicatorClass == "" {
			return fmt.Errorf("activity %d is incomplete or out of order", i+1)
		}
		proofs[activity.ProofChoice]++
		indicators[activity.IndicatorClass]++
	}
	for key, want := range map[string]int{"FOUNDATION": 4, "COHERENCE": 4, "REGRESSION": 4} {
		if proofs[key] != want || contract.ProofDistribution[key] != want {
			return fmt.Errorf("proof distribution %s is not %d", key, want)
		}
	}
	for key, want := range map[string]int{"DRIVER": 4, "OUTCOME": 4, "GUARDRAIL": 4} {
		if indicators[key] != want || contract.IndicatorDistribution[key] != want {
			return fmt.Errorf("indicator distribution %s is not %d", key, want)
		}
	}
	if len(contract.Cases) != 12 {
		return errors.New("contract must declare exactly 12 canonical cases")
	}
	return nil
}

func makeProgram(kind, sourceDigest string, baseline bool) string {
	semanticBranch := `if decision == "FIXED_POINT" {
		return outcome("CLOSED", "FIXED_POINT", nil, "")
	}
	return outcome("UNKNOWN", "FEEDBACK_COVERAGE_DECISION_UNKNOWN", unknownRecord(), "")`
	if baseline {
		semanticBranch = `if decision == "FIXED_POINT" {
		return outcome("CLOSED", "FIXED_POINT", nil, "")
	}
	return outcome("CLOSED", "FIXED_POINT", nil, "BASELINE_UNKNOWN_TOP_LEVEL_DEFAULTED_TO_FIXED_POINT")`
	}
	return strings.NewReplacer(
		"__KIND__", kind,
		"__SOURCE_DIGEST__", sourceDigest,
		"__SEMANTIC_BRANCH__", semanticBranch,
	).Replace(programTemplate)
}

const programTemplate = `package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

const (
	programKind = "__KIND__"
	semanticSourceSHA256 = "__SOURCE_DIGEST__"
	generatorIdentity = "gooo-loopgen@v0.1.0"
	evaluatorIdentity = "feedback-coverage-decision-evaluator@v0.1.0"
)

func main() {
	fixture := flag.String("fixture", "", "fixture JSON")
	output := flag.String("output", "", "caller-owned output JSON")
	scenario := flag.String("scenario", "", "scenario identity")
	flag.Parse()
	if *fixture == "" || *output == "" || *scenario == "" {
		fail("fixture, output, and scenario are required")
	}
	input, err := os.ReadFile(*fixture)
	if err != nil {
		fail(err.Error())
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		write(*output, outcome("REFUTED", "MALFORMED_INPUT", nil, "TOP_LEVEL_INPUT_IS_NOT_A_JSON_OBJECT"), *scenario, input)
		return
	}
	raw, ok := object["decision"]
	if !ok || string(raw) == "null" {
		write(*output, outcome("REFUTED", "MALFORMED_INPUT", nil, "TOP_LEVEL_DECISION_IS_MISSING_OR_NULL"), *scenario, input)
		return
	}
	var decision string
	if err := json.Unmarshal(raw, &decision); err != nil {
		write(*output, outcome("REFUTED", "MALFORMED_INPUT", nil, "TOP_LEVEL_DECISION_IS_NOT_A_STRING"), *scenario, input)
		return
	}
	write(*output, evaluate(decision), *scenario, input)
}

func evaluate(decision string) map[string]any {
	__SEMANTIC_BRANCH__
}

func unknownRecord() map[string]any {
	return map[string]any{
		"stage": "EVALUATE_TOP_LEVEL_DECISION",
		"step": "resolve-feedback-coverage-decision",
		"reason": "TOP_LEVEL_DECISION_IS_NOT_EXPLICIT_FIXED_POINT",
		"unknown_class": "FEEDBACK_COVERAGE_DECISION_UNKNOWN",
		"next_operation": "SUPPLY_EXPLICIT_FIXED_POINT_OR_HUMAN_FEEDBACK",
		"blocked_by": []string{"TOP_LEVEL_DECISION_NOT_EXPLICIT"},
	}
}

func outcome(resolution, decision string, unknown map[string]any, note string) map[string]any {
	result := map[string]any{
		"schema": "gooo/feedback-coverage-decision/outcome/v1",
		"program": programKind,
		"resolution": resolution,
		"decision": decision,
		"generator_identity": generatorIdentity,
		"evaluator_identity": evaluatorIdentity,
		"semantic_source_sha256": semanticSourceSHA256,
		"repository_writes": 0,
		"local_test_executions": 0,
		"cross_project_required_gates": 0,
	}
	if unknown != nil {
		result["unknown"] = unknown
	}
	if note != "" {
		result["baseline_note"] = note
	}
	return result
}

func write(path string, result map[string]any, scenario string, input []byte) {
	result["scenario"] = scenario
	sum := sha256.Sum256(input)
	result["fixture_sha256"] = hex.EncodeToString(sum[:])
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fail(err.Error())
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
`

func describe(path, kind string) (Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{Path: kind, Bytes: len(data), SHA256: digest(data), Kind: filepath.Ext(path)}, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
