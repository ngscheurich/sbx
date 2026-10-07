#!/bin/sh
set -eu
"$(dirname "$0")/setup.sh"
exec python3 -m http.server 4000 --bind 0.0.0.0 --directory /workspace
