#!/bin/sh
set -ue

find . -name 'go.mod' -execdir go mod tidy \;

if [ -n "$(git status --porcelain)" ]; then
    echo "go.mod is not up to date. Please 'run go mod tidy' and commit the changes."
    echo
    echo "The following go files did differ after tidying them:"
    git status --porcelain
    exit 1
fi