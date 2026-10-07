#!/bin/sh
set -eu
test -f "$SBX_DATA_DIR/bootstrapped"
printf 'ready\n' > "$SBX_DATA_DIR/ready"
