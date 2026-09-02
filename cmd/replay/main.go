package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	candidate := flag.String("candidate", "", "generated candidate Go file")
	fixture := flag.String("fixture", "", "fixture JSON")
	scenario := flag.String("scenario", "", "scenario identity")
	work := flag.String("work", "", "caller-owned temporary work directory")
	output := flag.String("output", "", "caller-owned replay receipt")
	goVersion := flag.String("go-version", "", "Go toolchain identity")
	flag.Parse()
	if *candidate == "" || *fixture == "" || *scenario == "" || *work == "" || *output == "" || *goVersion == "" {
		fail("candidate, fixture, scenario, work, output, and go-version are required")
	}
	if err := os.MkdirAll(*work, 0o755); err != nil {
		fail(err.Error())
	}
	first := filepath.Join(*work, "first.json")
	second := filepath.Join(*work, "second.json")
	run(*candidate, *fixture, *scenario, first)
	run(*candidate, *fixture, *scenario, second)
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		fail(err.Error())
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		fail(err.Error())
	}
	stable := bytes.Equal(firstBytes, secondBytes)
	fixtureBytes, err := os.ReadFile(*fixture)
	if err != nil {
		fail(err.Error())
	}
	candidateBytes, err := os.ReadFile(*candidate)
	if err != nil {
		fail(err.Error())
	}
	receipt := map[string]any{
		"schema": "gooo/replay-receipt/v1",
		"scenario": *scenario,
		"fixture_sha256": digest(fixtureBytes),
		"candidate_sha256": digest(candidateBytes),
		"replay_count": 2,
		"stable": stable,
		"go_toolchain": *goVersion,
		"repository_writes": 0,
		"local_test_executions": 0,
		"cross_project_required_gates": 0,
	}
	if !stable {
		receipt["status"] = "REFUTED"
		receipt["reason"] = "CANDIDATE_REPLAY_BYTES_DIFFER"
	} else {
		receipt["status"] = "CLOSED"
	}
	writeJSON(*output, receipt)
	_ = os.Remove(first)
	_ = os.Remove(second)
}

func run(candidate, fixture, scenario, output string) {
	cmd := exec.Command("go", "run", candidate, "--fixture", fixture, "--output", output, "--scenario", scenario)
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	if outputBytes, err := cmd.CombinedOutput(); err != nil {
		fail(fmt.Sprintf("candidate replay failed: %v: %s", err, outputBytes))
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeJSON(path string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
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
