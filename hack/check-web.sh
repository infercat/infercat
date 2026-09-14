#!/bin/sh
# Check-only ports/cache/evidence are isolated; direct dev commands retain their defaults.
set -eu
role=$1; shift
case "$role" in
  launch) offset=0 ;; compat) offset=1 ;; test) offset=4 ;; build) offset=5 ;;
  *) echo "unknown check role: $role" >&2; exit 1 ;;
esac
CHECK_PORT=$(( ${CHECK_PORT:-6833} + offset ))
CHECK_CACHE_DIR="$PWD/node_modules/.vite-$role"
check_evidence=$(mktemp -d)
trap 'rm -rf "$check_evidence"' EXIT
COMPAT_SHOTS=$check_evidence LAUNCH_SHOTS=$check_evidence UPDATE_SHOTS=$check_evidence
export CHECK_PORT CHECK_CACHE_DIR COMPAT_SHOTS LAUNCH_SHOTS UPDATE_SHOTS
"$@"
