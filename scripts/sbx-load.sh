#!/usr/bin/env bash
# Builds the Docker image and loads it into microsandbox.
#
# Usage:
#   scripts/sbx-load.sh
#
# `msb load --input` spelling verified against msb 0.7.6 (stdin piping is
# also supported, but the pinned surface names --input).

set -euo pipefail

archive="$(mktemp "${TMPDIR:-/tmp}/sbx-dev-XXXXXX.tar")"
trap 'rm -f "$archive"' EXIT

docker build -t sbx-dev .
docker save sbx-dev -o "$archive"
msb load --input "$archive"
