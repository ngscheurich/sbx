#!/bin/sh
set -eu
command -v python3 >/dev/null
test ! -e /workspace/index.html
