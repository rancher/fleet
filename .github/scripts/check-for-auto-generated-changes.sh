#!/bin/bash
set -euo pipefail

go generate
ginkgo unfocus

if [ -n "$(git status --porcelain)" ]; then
    printf 'Generated files have either been changed manually or were not updated.\n\n'

    echo "The following generated files did differ after regeneration:"
    git status --porcelain
    exit 1
fi
