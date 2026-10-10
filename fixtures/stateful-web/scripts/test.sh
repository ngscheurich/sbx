#!/bin/sh
set -eu
"$(dirname "$0")/setup.sh"
test -f "$SBX_DATA_DIR/ready"
printf 'built\n' > /vm/artifacts/result
