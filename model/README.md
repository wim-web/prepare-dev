# prepare_model — Lean 4 model of SPEC-0001

Executable state machine (`PrepareDev/Spec/State.lean`, `Step.lean`), reachability (`Reachable.lean`),
an inductive invariant (`Invariant.lean`) and the claims C1–C3 as theorems (`Theorems.lean`:
`applied_at_most_once`, `committed_matches_snapshot`, `quiescent_consistent`, plus `step_commit_guard`, `answers_monotone`).
Core Lean only (toolchain in `lean-toolchain`, no Mathlib).

- Build and check: `bin/test` (token scan, `lake build`, axiom audit, generator smoke test).
- Generate conformance traces (SPEC-0001 Section 4, JSON Lines on stdout):
  `lake exe prepare-model gen --depth 6 [--max-traces 2000] [--seed 0]`
  Enumeration is a deterministic DFS over R1/R2, Q1/Q2, A1/A2; `--seed` rotates the action order.
