#!/usr/bin/env bash
set -euo pipefail

: "${R2_BUCKET:?R2_BUCKET is required}"
: "${R2_ENDPOINT:?R2_ENDPOINT is required}"

retry_cmd() {
  local max_attempts="${R2_AWS_MAX_ATTEMPTS:-5}"
  local delay="${R2_AWS_RETRY_DELAY:-2}"
  local attempt=1
  local rc
  while true; do
    rc=0
    "$@" || rc=$?
    if [ "$rc" -eq 0 ]; then
      return 0
    fi
    if [ "$attempt" -ge "$max_attempts" ]; then
      return "$rc"
    fi
    echo "retry $attempt/$max_attempts for: $*" >&2
    sleep "$delay"
    attempt=$((attempt + 1))
    delay=$((delay * 2))
  done
}

aws_cmd() {
  retry_cmd aws --cli-connect-timeout 10 --cli-read-timeout 300 --endpoint-url "$R2_ENDPOINT" "$@"
}

usage() {
  echo "Usage: $0 <upload|download|copy> ..." >&2
  echo "  upload <local_path> <bucket_key>" >&2
  echo "  download <bucket_key> <local_path_or_dir>" >&2
  echo "  copy <source_bucket_key> <target_bucket_key>" >&2
}

case "${1:-}" in
  upload)
    [ "$#" -eq 3 ] || { usage; exit 1; }
    src="$2"
    key="$3"
    aws_cmd s3 cp "$src" "s3://${R2_BUCKET}/${key}"
    ;;
  download)
    [ "$#" -eq 3 ] || { usage; exit 1; }
    key="$2"
    dest="$3"
    if [ -d "$dest" ]; then
      dest="${dest%/}/$(basename "$key")"
    fi
    mkdir -p "$(dirname "$dest")"
    aws_cmd s3 cp "s3://${R2_BUCKET}/${key}" "$dest"
    ;;
  copy)
    [ "$#" -eq 3 ] || { usage; exit 1; }
    src_key="$2"
    dst_key="$3"
    tmp_dir=$(mktemp -d)
    trap 'rm -rf "$tmp_dir"' EXIT
    tmp_file="$tmp_dir/$(basename "$src_key")"
    aws_cmd s3 cp "s3://${R2_BUCKET}/${src_key}" "$tmp_file"
    aws_cmd s3 cp "$tmp_file" "s3://${R2_BUCKET}/${dst_key}"
    rm -rf "$tmp_dir"
    trap - EXIT
    ;;
  *)
    usage
    exit 1
    ;;
esac
