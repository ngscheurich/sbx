#!/usr/bin/env bash
# Runs the Pi coding agent in a sandbox.
#
# Usage:
#   scripts/msb-pi.sh
#   scripts/msb-pi.sh "some prompt"

set -eu

msb run sbx-dev \
  --cpus 2 \
  --memory 2G \
  --volume ".:/workspace" \
  --volume "${HOME}/AGENTS.md:/root/AGENTS.md:ro" \
  --volume "${HOME}/.agents:/root/.agents:ro" \
  --volume "${HOME}/.pi/agent/auth.json:/root/.pi/agent/auth.json:ro" \
  --volume "${HOME}/.pi/agent/settings.json:/root/.pi/agent/settings.json:ro" \
  --volume "${HOME}/.pi/agent/sessions:/root/.pi/agent/sessions" \
  --workdir /workspace \
  -- bash -ic "pi --approve $@"
