#!/bin/sh
set -ue

go generate
ginkgo unfocus

if [ -n "$(git status --porcelain)" ]; then
    echo "Generated files have either been changed manually or were not updated.\n"

    echo "The following generated files did differ after regeneration:"
    git status --porcelain
    exit 1
fi
