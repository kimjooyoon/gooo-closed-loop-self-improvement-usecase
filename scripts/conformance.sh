#!/usr/bin/env bash
set -euo pipefail

if [[ "${GITHUB_ACTIONS:-}" != "true" ]]; then
  echo "conformance is Actions-only" >&2
  exit 1
fi

repo_root="${GITHUB_WORKSPACE:?GITHUB_WORKSPACE is required}"
out="${1:-${OUTPUT_DIR:?OUTPUT_DIR is required}}"
mkdir -p "$out/generated" "$out/baseline" "$out/candidate" "$out/evidence" "$out/replay-work" "$out/upstream"

test "$(find "$out/generated" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d ' ')" = "0"
test "$(find "$out/evidence" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d ' ')" = "0"
go_version="$(go version)"

go run ./cmd/loopgen generate \
  --source "$repo_root/.gooo/closed-loop.gooo" \
  --contract "$repo_root/contracts/denominator-v1.json" \
  --out "$out/generated" \
  --repo-root "$repo_root"

gofmt -w "$out/generated/baseline.go" "$out/generated/candidate.go"
gofmt -d cmd/loopgen/main.go cmd/assemble/main.go cmd/replay/main.go "$out/generated/baseline.go" "$out/generated/candidate.go" > "$out/gofmt.diff"
test ! -s "$out/gofmt.diff"

while IFS= read -r row; do
  ordinal=$(jq -r '.ordinal' <<<"$row")
  fixture=$(jq -r '.fixture' <<<"$row")
  scenario=$(jq -r '.id' <<<"$row")
  baseline_output="$out/baseline/$(printf '%02d' "$ordinal").json"
  candidate_output="$out/candidate/$(printf '%02d' "$ordinal").json"
  if [[ "$ordinal" = "2" ]]; then
    /usr/bin/time -f '%e %M' -o "$out/measurement-before.txt" go run "$out/generated/baseline.go" --fixture "$repo_root/$fixture" --output "$baseline_output" --scenario "$scenario"
    /usr/bin/time -f '%e %M' -o "$out/measurement-after.txt" go run "$out/generated/candidate.go" --fixture "$repo_root/$fixture" --output "$candidate_output" --scenario "$scenario"
  else
    go run "$out/generated/baseline.go" --fixture "$repo_root/$fixture" --output "$baseline_output" --scenario "$scenario"
    go run "$out/generated/candidate.go" --fixture "$repo_root/$fixture" --output "$candidate_output" --scenario "$scenario"
  fi
done < <(jq -c '.cases[]' "$repo_root/contracts/denominator-v1.json")

go run ./cmd/replay \
  --candidate "$out/generated/candidate.go" \
  --fixture "$repo_root/fixtures/unknown-top-level.json" \
  --scenario "C02-UNKNOWN-TOP-LEVEL" \
  --work "$out/replay-work" \
  --output "$out/replay-receipt.json" \
  --go-version "$go_version"

scripts/verify-upstreams.sh "$out/upstream"

go run ./cmd/assemble \
  --contract "$repo_root/contracts/denominator-v1.json" \
  --repo-root "$repo_root" \
  --generated "$out/generated" \
  --baseline-dir "$out/baseline" \
  --candidate-dir "$out/candidate" \
  --replay "$out/replay-receipt.json" \
  --measurement-before "$out/measurement-before.txt" \
  --measurement-after "$out/measurement-after.txt" \
  --upstream "$out/upstream/upstream-verification.json" \
  --out "$out/evidence" \
  --go-version "$go_version" \
  --run-id "${GITHUB_RUN_ID:-unknown}" \
  --event "${GITHUB_EVENT_NAME:-unknown}" \
  --commit "${GITHUB_SHA:-unknown}"

jq -e '.decision == "CLOSED" and .denominator == 12 and .activity_count == 12 and .generated_artifact_count == 7 and .proof_distribution == {FOUNDATION:4,COHERENCE:4,REGRESSION:4} and .indicator_distribution == {DRIVER:4,OUTCOME:4,GUARDRAIL:4}' "$out/evidence/conformance-report.json" >/dev/null
jq -e '.before.decision == "FIXED_POINT" and .after.decision == "FEEDBACK_COVERAGE_DECISION_UNKNOWN" and .status == "CLOSED"' "$out/evidence/before-after-comparison.json" >/dev/null
jq -e '.status == "CLOSED" and .stable == true and .replay_count == 2' "$out/replay-receipt.json" >/dev/null
jq -e '.verified_count == 5 and (.composition_boundary.status == "UNKNOWN")' "$out/upstream/upstream-verification.json" >/dev/null

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  echo "output_dir=$out" >> "$GITHUB_OUTPUT"
fi
echo "caller-owned evidence: $out/evidence"
