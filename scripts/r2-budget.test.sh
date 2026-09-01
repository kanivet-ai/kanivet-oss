#!/usr/bin/env bash
# Unit test for retry_cmd in scripts/r2-budget.sh.
# Run with: bash scripts/r2-budget.test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
export R2_BUCKET="test-bucket"
export R2_ENDPOINT="https://example.invalid"

# shellcheck disable=SC1091
source "$SCRIPT_DIR/r2-budget.sh"

pass=0
fail=0

expect() {
  local name="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    echo "PASS: $name"
    pass=$((pass+1))
  else
    echo "FAIL: $name — expected=$expected actual=$actual"
    fail=$((fail+1))
  fi
}

_attempts_seen=0
_failing_stub_for_n() {
  _attempts_seen=$((_attempts_seen+1))
  local threshold="${STUB_FAIL_UNTIL:-0}"
  if [ "$_attempts_seen" -le "$threshold" ]; then
    return 1
  fi
  echo "stub-success"
  return 0
}

# Using a tmpfile for stdout keeps retry_cmd running in the parent shell so
# the test can observe _attempts_seen (command substitution would subshell it).
tmpf=$(mktemp)
trap 'rm -f "$tmpf"' EXIT

# 1. happy path: single-try success
_attempts_seen=0
STUB_FAIL_UNTIL=0
retry_cmd 3 0 _failing_stub_for_n >"$tmpf"
out=$(<"$tmpf")
expect "retry_happy_path_output"    "stub-success" "$out"
expect "retry_happy_path_attempts"  "1"            "$_attempts_seen"

# 2. transient: fails twice then succeeds on 3rd
_attempts_seen=0
STUB_FAIL_UNTIL=2
retry_cmd 3 0 _failing_stub_for_n >"$tmpf"
out=$(<"$tmpf")
expect "retry_transient_output"    "stub-success" "$out"
expect "retry_transient_attempts"  "3"            "$_attempts_seen"

# 3. always fails: gives up after max attempts with non-zero rc
_attempts_seen=0
STUB_FAIL_UNTIL=99
set +e
retry_cmd 3 0 _failing_stub_for_n >/dev/null
rc=$?
set -e
expect "retry_gives_up_rc"        "1"  "$rc"
expect "retry_gives_up_attempts"  "3"  "$_attempts_seen"

list_objects_json() {
  printf '%s' '{"Contents":[{"Key":"kanivet-standalone-setup-0.32.0.exe","LastModified":"2026-01-01T00:00:00Z"},{"Key":"kanivet-standalone-setup-0.33.0.exe","LastModified":"2026-02-01T00:00:00Z"}]}'
}
protected_feed_versions() { return 0; }
removed_keys=()
remove_key() { removed_keys+=("$1"); }
trim_versioned_objects "" 1
expect "trim_old_standalone_setup" "kanivet-standalone-setup-0.32.0.exe" "${removed_keys[*]:-}"

echo "---"
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
