#!/bin/bash
set -o pipefail
env
"$@" 2>&1 | tee /dev/termination-log
