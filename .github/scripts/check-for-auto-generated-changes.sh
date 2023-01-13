#!/bin/sh
set -ue

go run pkg/codegen/cleanup/main.go > /dev/null
go run pkg/codegen/main.go

go run ./pkg/codegen crds ./charts/fleet-crd/templates/crds.yaml > /dev/null

if [[ $(git status --porcelain) ]]; then
    echo -e "Generated files have either been changed manually or were not updated.\n"

    echo "The following generated files did differ after regeneration:"
    git status --porcelain
    exit 1
fi
