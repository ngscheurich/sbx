#!/usr/bin/env bash
# Runs the Pi coding agent in a sandbox.
#
# Usage:
#   scripts/msb-pi.sh
#   scripts/msb-pi.sh "some prompt"
#
# Flag spellings follow sbx's pinned msb surface (--mount-dir for
# directories, --mount-file for files), verified against msb 0.7.6.

set -eu

msb run sbx-dev \
  --cpus 2 \
  --memory 2G \
  --mount-dir ".:/workspace" \
  --mount-file "${HOME}/AGENTS.md:/root/AGENTS.md:ro" \
  --mount-dir "${HOME}/.agents:/root/.agents:ro" \
  --mount-file "${HOME}/.pi/agent/auth.json:/root/.pi/agent/auth.json:ro" \
  --mount-file "${HOME}/.pi/agent/settings.json:/root/.pi/agent/settings.json:ro" \
  --mount-dir "${HOME}/.pi/agent/sessions:/root/.pi/agent/sessions" \
  --workdir /workspace \
  -- bash -ic 'pi --approve "$@"' _ "$@"
