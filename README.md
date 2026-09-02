# Gooo closed-loop self-improvement use case

This repository closes one deliberately small, bounded self-improvement loop.
The authoritative `.gooo` source declares a decision rule and twelve activities.
The generated candidate repairs one semantic bug: an unknown top-level decision
must not be treated as `FIXED_POINT`.

The pinned baseline returns `CLOSED / FIXED_POINT` for `{"decision":"MAYBE"}`.
The generated candidate returns `UNKNOWN /
FEEDBACK_COVERAGE_DECISION_UNKNOWN` with exactly these six fields:
`stage`, `step`, `reason`, `unknown_class`, `next_operation`, and `blocked_by`.
Only an explicit `FIXED_POINT` may close. Resolution precedence is
`REFUTED > UNKNOWN > CLOSED`.

The denominator is fixed at 12 activities. Proof and indicator buckets are
both exactly `FOUNDATION / COHERENCE / REGRESSION = 4 / 4 / 4` and
`DRIVER / OUTCOME / GUARDRAIL = 4 / 4 / 4`. No scalar score is produced.

All generation, formatting, build, vet, test, conformance, replay, upstream
release verification, and measurements run only in GitHub Actions. Runtime
repository writes, local test executions, and cross-project required gates are
all zero. Generated candidates and evidence are written only to caller-owned
temporary output; no input repository is edited by the loop.

The five released Gooo mechanisms in `contracts/upstream-release-locks.json`
are optional immutable metadata/digest inputs. Their releases and assets are
verified in Actions. Their formats are not fabricated into this fixture, so
that external composition boundary remains an explicit six-field `UNKNOWN`.

The generated candidate is never automatically adopted. Human adoption,
performance, and memory improvement claims remain separate boundaries.
