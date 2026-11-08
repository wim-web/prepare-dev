#!/usr/bin/env bash
# End-to-end check of SPEC-0001 recovery with real daemon processes (M1 criteria 8-10):
#   1. crash after stage c (question file written, commit not written), edit the subject while
#      down, restart -> abort: no public decision, answer pending and stale
#   2. crash after stage d (commit written, no response), edit the subject, retry -> same decision
set -euo pipefail
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cli() { pnpm --silent --dir "$repo" cli "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

run_case() { # $1 = crash stage, $2 = expected outcome (abort|committed)
  local stage="$1" expect="$2"
  local T; T="$(mktemp -d /tmp/pd-e2e-XXXX)"
  cli init --root "$T" >/dev/null
  pnpm --silent --dir "$repo" cli serve --root "$T" --crash-after "$stage" >"$T/serve1.log" 2>&1 &
  for _ in $(seq 1 60); do [ -f "$T/.prepare-dev/daemon.json" ] && break; sleep 0.25; done
  [ -f "$T/.prepare-dev/daemon.json" ] || fail "daemon did not start"
  cli record create --root "$T" --kind requirement --title "R" --body "v1" >/dev/null
  cli q create --root "$T" --prompt "?" --subject REQ-0001 --option a=A >/dev/null
  cli q answer --root "$T" Q-0001 --choice a >/dev/null
  cli q apply --root "$T" Q-0001 ANS-0001-1 --title "A" --body "b" >/dev/null 2>&1 || true   # the daemon exits here
  sleep 0.5
  grep -q "simulated crash after stage $stage" "$T/serve1.log" || fail "daemon did not crash at stage $stage"
  rm -f "$T/.prepare-dev/daemon.json"
  printf '\n停止中に編集\n' >> "$T/prepare/requirements/REQ-0001.md"
  cli serve --root "$T" --detach >/dev/null
  local status; status="$(cli status --root "$T" --json)"
  local retry; retry="$(cli q apply --root "$T" Q-0001 ANS-0001-1 --title "A" --body "b" 2>&1 || true)"
  cli stop --root "$T" >/dev/null
  case "$expect" in
    abort)
      grep -q '"type":"apply.abort"' "$T/prepare/events.jsonl" || fail "no abort recorded"
      [ ! -e "$T/prepare/decisions/DEC-0001.md" ] || fail "provisional decision still on disk"
      echo "$status" | grep -q '"stale": true' || fail "answer not stale after abort"
      echo "$status" | grep -q '"answer_id": "ANS-0001-1"' || fail "answer not pending after abort"
      echo "$retry" | grep -q 'stale' || fail "retry after abort should be rejected as stale: $retry"
      ;;
    committed)
      grep -q '"type":"apply.commit"' "$T/prepare/events.jsonl" || fail "no commit recorded"
      ! grep -q '"type":"apply.abort"' "$T/prepare/events.jsonl" || fail "committed transaction was aborted"
      [ -e "$T/prepare/decisions/DEC-0001.md" ] || fail "committed decision missing"
      echo "$retry" | grep -q '"status": "already_applied"' || fail "retry should return already_applied: $retry"
      echo "$retry" | grep -q '"decision_id": "DEC-0001"' || fail "retry returned a different decision"
      echo "$status" | grep -q '"pending": \[\]' || fail "committed answer still pending"
      ;;
  esac
  echo "ok: crash after $stage -> $expect ($T)"
}

run_case question abort
run_case commit committed
echo "e2e crash/recovery: all cases passed"
