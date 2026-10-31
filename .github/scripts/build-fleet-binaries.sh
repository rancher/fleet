#!/bin/bash
# Description: build fleet binary and image with debug flags

set -euxo pipefail

export GOARCH="${GOARCH:-amd64}"
export CGO_ENABLED=0
export GOOS=linux

# The controller only hot-reloads its config map if this version matches the
# chart's image tag, so set FLEET_VERSION when images use a tag other than "dev".
ldflags=""
if [ -n "${FLEET_VERSION:-}" ]; then
  ldflags="-X github.com/rancher/fleet/pkg/version.Version=${FLEET_VERSION}"
fi

# fleet
go build -gcflags='all=-N -l' -ldflags "$ldflags" -o bin/fleetcontroller-linux-"$GOARCH" ./cmd/fleetcontroller

# fleet agent
go build -gcflags='all=-N -l' -ldflags "$ldflags" -o "bin/fleet-linux-$GOARCH" ./cmd/fleetcli
go build -gcflags='all=-N -l' -ldflags "$ldflags" -o "bin/fleetagent-linux-$GOARCH" ./cmd/fleetagent
