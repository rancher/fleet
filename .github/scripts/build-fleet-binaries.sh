#!/bin/bash
# Description: build fleet binary and image with debug flags

set -euxo pipefail

export GOARCH="${GOARCH:-amd64}"
export CGO_ENABLED=0
export GOOS=linux

# fleet
go build -gcflags='all=-N -l' -o bin/fleetcontroller-linux-"$GOARCH" ./cmd/fleetcontroller

# fleet agent
go build -gcflags='all=-N -l' -o "bin/fleet-linux-$GOARCH" ./cmd/fleetcli
go build -gcflags='all=-N -l' -o "bin/fleetagent-linux-$GOARCH" ./cmd/fleetagent
