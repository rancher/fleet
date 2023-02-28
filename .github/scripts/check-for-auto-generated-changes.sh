#!/bin/sh
set -ue

go run pkg/codegen/cleanup/main.go > /dev/null
go run pkg/codegen/main.go

go run ./pkg/codegen crds ./charts/fleet-crd/templates/crds.yaml > /dev/null

if [ -n "$(git status --porcelain)" ]; then
    printf "Generated files have either been changed manually or were not updated.\n"

    printf "The following generated files did differ after regeneration:"
    git status --porcelain
    exit 1
fi
