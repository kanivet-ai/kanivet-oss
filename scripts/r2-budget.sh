#!/usr/bin/env bash
set -euo pipefail

if [ -z "${BASH_VERSION:-}" ]; then
  echo "This script must be run with bash" >&2
  exit 2
fi

: "${R2_BUCKET:?R2_BUCKET is required}"
MAX_BYTES="${R2_MAX_BYTES:-5368709120}"
KEEP_MAIN_RELEASES="${R2_KEEP_MAIN_RELEASES:-2}"
KEEP_BRANCH_RELEASES="${R2_KEEP_BRANCH_RELEASES:-1}"
KEEP_BRANCH_NAMES="${R2_KEEP_BRANCH_NAMES:-5}"
CURRENT_VERSION="${APP_VERSION:-}"

log() {
  echo "[r2-budget] $*" >&2
}

aws_json() {
  aws --cli-connect-timeout 10 --cli-read-timeout 30 "$@"
}

# retry_cmd MAX_ATTEMPTS INITIAL_DELAY_SECONDS CMD [ARGS...]
# Invokes CMD with ARGS. If it exits non-zero, retries up to MAX_ATTEMPTS total
# with exponential backoff starting at INITIAL_DELAY_SECONDS. Propagates the
# command's stdout on success and its final exit status on failure.
retry_cmd() {
  local max_attempts="$1"; shift
  local delay="$1"; shift
  local attempt=1
  local rc
  while true; do
    rc=0
    "$@" || rc=$?
    if [ "$rc" -eq 0 ]; then return 0; fi
    if [ "$attempt" -ge "$max_attempts" ]; then return "$rc"; fi
    log "command failed (attempt ${attempt}/${max_attempts}); retrying in ${delay}s..."
    sleep "$delay"
    attempt=$((attempt+1))
    delay=$((delay*2))
  done
}

aws_list_objects() {
  local prefix="$1"
  shift
  local args=(s3api list-objects-v2 --bucket "$R2_BUCKET" --endpoint-url "$R2_ENDPOINT" --output json)
  if [ -n "$prefix" ]; then
    args+=(--prefix "$prefix")
  fi
  args+=("$@")
  aws_json_capture "${args[@]}"
}

_aws_json_capture_once() {
  local tmp_out tmp_err status
  tmp_out=$(mktemp)
  tmp_err=$(mktemp)
  set +e
  aws_json "$@" >"$tmp_out" 2>"$tmp_err"
  status=$?
  set -e
  if [ "$status" -ne 0 ]; then
    echo "[r2-budget] aws command failed: aws $*" >&2
    [ -s "$tmp_err" ] && cat "$tmp_err" >&2
    [ -s "$tmp_out" ] && { echo "[r2-budget] aws stdout:" >&2; cat "$tmp_out" >&2; }
    rm -f "$tmp_out" "$tmp_err"
    return "$status"
  fi
  if [ ! -s "$tmp_out" ]; then
    printf '{"Contents":[]}'
    rm -f "$tmp_out" "$tmp_err"
    return 0
  fi
  if ! python3 -m json.tool "$tmp_out" >/dev/null 2>&1; then
    echo "[r2-budget] aws command returned non-JSON stdout: aws $*" >&2
    [ -s "$tmp_err" ] && { echo "[r2-budget] aws stderr:" >&2; cat "$tmp_err" >&2; }
    echo "[r2-budget] aws stdout:" >&2
    cat "$tmp_out" >&2
    rm -f "$tmp_out" "$tmp_err"
    return 1
  fi
  cat "$tmp_out"
  rm -f "$tmp_out" "$tmp_err"
}

aws_json_capture() {
  retry_cmd "${R2_AWS_MAX_ATTEMPTS:-3}" "${R2_AWS_RETRY_DELAY:-2}" _aws_json_capture_once "$@"
}

require_aws() {
  if ! command -v aws >/dev/null 2>&1; then
    echo "aws CLI is required" >&2
    exit 1
  fi
}

list_objects_json() {
  local prefix="$1"
  log "listing objects for prefix '${prefix:-<root>}'"
  aws_list_objects "$prefix"
}

protected_feed_versions() {
  local keys=(latest-mac.yml standalone-mac.yml latest.yml)
  local key version
  for key in "${keys[@]}"; do
    version="$(aws s3 cp "s3://${R2_BUCKET}/${key}" - --endpoint-url "$R2_ENDPOINT" 2>/dev/null | awk '$1 == "version:" { print $2; exit }' || true)"
    [ -n "$version" ] && echo "$version"
  done | sort -u
  return 0
}

sum_prefix_bytes() {
  local prefix="$1" json
  json="$(list_objects_json "$prefix")" || return 1
  printf '%s' "$json" | python3 -c 'import sys, json; data=json.load(sys.stdin); print(sum(obj.get("Size", 0) for obj in data.get("Contents", [])))'
}

prefix_mtime() {
  local prefix="$1" json
  log "calculating latest modified time for '${prefix:-<root>}'"
  json="$(list_objects_json "$prefix")" || return 1
  printf '%s' "$json" | python3 -c 'import sys, json; data=json.load(sys.stdin); items=data.get("Contents", []); print(max([i.get("LastModified", "") for i in items] + [""]))'
}

remove_prefix() {
  local prefix="$1"
  echo "🗑 Removing prefix: $prefix"
  aws_json s3 rm "s3://${R2_BUCKET}/${prefix}" --recursive --endpoint-url "$R2_ENDPOINT"
}

remove_key() {
  local key="$1"
  echo "🗑 Removing object: $key"
  aws_json s3 rm "s3://${R2_BUCKET}/${key}" --endpoint-url "$R2_ENDPOINT"
}

trim_versioned_objects() {
  local prefix="$1"
  local keep_versions="$2"
  local json
  json="$(list_objects_json "$prefix")" || return 1

  local protected_versions
  protected_versions="$(protected_feed_versions)"
  local keys_to_remove=()
  while IFS= read -r line; do keys_to_remove+=("$line"); done < <(
    printf '%s' "$json" | python3 -c 'import json, sys, re
keep = int(sys.argv[1])
current = sys.argv[2]
protected = set(filter(None, sys.argv[3].splitlines()))
data = json.load(sys.stdin)
items = data.get("Contents", [])
regular_re = re.compile(r"^(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?(?:\+[0-9A-Za-z.]+)?)(?=-(?:arm64|x64|universal|ia32)(?:-|\.|$))")
setup_re = re.compile(r"^(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?(?:\+[0-9A-Za-z.]+)?)(?=\.)")
versions = {}
for item in items:
    key = item.get("Key", "")
    name = key.rsplit("/", 1)[-1]
    version = None
    if name.startswith("kanivet-standalone-setup-"):
        match = setup_re.match(name[len("kanivet-standalone-setup-"):])
        version = match.group(1) if match else None
    elif name.startswith("kanivet-standalone-"):
        match = regular_re.match(name[len("kanivet-standalone-"):])
        version = match.group(1) if match else None
    elif name.startswith("kanivet-setup-"):
        match = setup_re.match(name[len("kanivet-setup-"):])
        version = match.group(1) if match else None
    elif name.startswith("kanivet-"):
        match = regular_re.match(name[len("kanivet-"):])
        version = match.group(1) if match else None
    if not version:
        continue
    versions.setdefault(version, {"mtime": "", "keys": []})
    versions[version]["mtime"] = max(versions[version]["mtime"], item.get("LastModified", ""))
    versions[version]["keys"].append(key)
ordered = sorted(versions.items(), key=lambda kv: kv[1]["mtime"], reverse=True)
has_current = bool(current) and current in versions
remaining_keep = keep - 1 if has_current and keep > 0 else keep
kept = 0
for version, meta in ordered:
    if current and version == current:
        continue
    if version in protected:
        continue
    if kept < remaining_keep:
        kept += 1
        continue
    for key in meta["keys"]:
        print(key)' "$keep_versions" "$CURRENT_VERSION" "$protected_versions"
  )

  [ ${#keys_to_remove[@]} -eq 0 ] && return 0
  for key in "${keys_to_remove[@]}"; do
    remove_key "$key"
  done
}

list_branch_prefixes() {
  local json
  json="$(list_objects_json "branch/")" || return 1
  printf '%s' "$json" | python3 -c 'import json, sys
data = json.load(sys.stdin)
prefixes = set()
for obj in data.get("Contents", []):
    key = obj.get("Key", "")
    if not key.startswith("branch/"):
        continue
    rel = key[len("branch/"):]
    if "/releases/" in rel:
        branch_path = rel.split("/releases/", 1)[0]
    elif "/" in rel:
        branch_path = rel.rsplit("/", 1)[0]
    else:
        continue
    prefixes.add(f"branch/{branch_path}/")
for prefix in sorted(prefixes):
    print(prefix)'
}

sorted_branch_prefixes() {
  local branches=()
  while IFS= read -r line; do branches+=("$line"); done < <(list_branch_prefixes)
  [ ${#branches[@]} -eq 0 ] && return 0

  for prefix in "${branches[@]}"; do
    ts=$(prefix_mtime "$prefix")
    printf '%s\t%s\n' "$ts" "$prefix"
  done | sort -r | cut -f2-
}

trim_root_releases() {
  log "trimming root release files, keeping ${KEEP_MAIN_RELEASES} version sets"
  trim_versioned_objects "" "$KEEP_MAIN_RELEASES"
}

trim_old_branches() {
  log "trimming branch prefixes, keeping ${KEEP_BRANCH_NAMES} branches and ${KEEP_BRANCH_RELEASES} version set(s) each"
  local sorted=()
  while IFS= read -r line; do sorted+=("$line"); done < <(sorted_branch_prefixes)
  [ ${#sorted[@]} -eq 0 ] && return 0

  local idx=0
  for prefix in "${sorted[@]}"; do
    idx=$((idx + 1))
    if [ "$idx" -le "$KEEP_BRANCH_NAMES" ]; then
      echo "Keeping branch prefix: $prefix"
      trim_versioned_objects "$prefix" "$KEEP_BRANCH_RELEASES"
      continue
    fi
    remove_prefix "$prefix"
  done
}

force_trim_oldest_branches_until_under_budget() {
  local usage="$1"
  log "usage still above budget, pruning oldest branch prefixes until under limit"
  local sorted=()
  while IFS= read -r line; do sorted+=("$line"); done < <(sorted_branch_prefixes)
  [ ${#sorted[@]} -eq 0 ] && return 0

  local prefix
  for (( idx=${#sorted[@]}-1; idx>=0; idx-- )); do
    if [ "$usage" -le "$MAX_BYTES" ]; then
      break
    fi
    prefix="${sorted[$idx]}"
    remove_prefix "$prefix"
    usage="$(total_bytes)" || return 1
    echo "📦 R2 usage after removing ${prefix}: ${usage} bytes"
  done
}

total_bytes() {
  sum_prefix_bytes ""
}

enforce_budget() {
  require_aws
  log "starting budget enforcement"
  local usage
  usage="$(total_bytes)" || return 1
  echo "📦 Current R2 usage: ${usage} bytes"

  trim_root_releases
  trim_old_branches

  usage="$(total_bytes)" || return 1
  echo "📦 R2 usage after retention trim: ${usage} bytes"

  if [ "$usage" -gt "$MAX_BYTES" ]; then
    force_trim_oldest_branches_until_under_budget "$usage"
    usage="$(total_bytes)" || return 1
    echo "📦 R2 usage after emergency branch trim: ${usage} bytes"
  fi

  if [ "$usage" -gt "$MAX_BYTES" ]; then
    echo "❌ R2 usage ${usage} exceeds max ${MAX_BYTES} bytes after cleanup" >&2
    exit 1
  fi
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  case "${1:-enforce}" in
    enforce)
      enforce_budget
      ;;
    usage)
      require_aws
      total_bytes
      ;;
    *)
      echo "Usage: $0 [enforce|usage]" >&2
      exit 1
      ;;
  esac
fi
