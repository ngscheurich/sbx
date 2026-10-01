#!/usr/bin/env bash
# Builds the Docker image and loads it into microsandbox.
#
# Usage:
#   scripts/sbx-load.sh

set -euo pipefail

docker build -t sbx-dev .
docker save sbx-dev | msb load
