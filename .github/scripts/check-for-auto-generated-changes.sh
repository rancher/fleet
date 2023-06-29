#!/bin/sh
set -ue

go run cmd/codegen/cleanup/main.go > /dev/null
go run cmd/codegen/main.go

go run ./cmd/codegen crds ./charts/fleet-crd/templates/crds.yaml > /dev/null

if [ -n "$(git status --porcelain)" ]; then
    echo "Generated files have either been changed manually or were not updated.\n"

    echo "The following generated files did differ after regeneration:"
    git status --porcelain
    exit 1
fi
