#!/bin/bash
# Description: build fleet binary and image with debug flags

set -euxo pipefail

if [ ! -d ./cmd/fleetcontroller ]; then
  echo "please change the current directory to the fleet repo checkout"
  exit 1
fi

export GOARCH="${GOARCH:-amd64}"
export CGO_ENABLED=0

# re-generate code
if ! git diff --quiet HEAD origin/master --  pkg/apis/fleet.cattle.io/v1alpha1; then
  go generate
fi

export GOOS=linux
# fleet
go build -gcflags='all=-N -l' -o bin/fleetcontroller-linux-"$GOARCH" ./cmd/fleetcontroller

# fleet agent
go build -gcflags='all=-N -l' -o "bin/fleet-linux-$GOARCH" ./cmd/fleetcli
go build -gcflags='all=-N -l' -o "bin/fleetagent-linux-$GOARCH" ./cmd/fleetagent

# gitjob
go build -gcflags='all=-N -l' -o "bin/gitcloner" ./cmd/gitcloner
go build -gcflags='all=-N -l' -o "bin/gitjob" ./cmd/gitjob
