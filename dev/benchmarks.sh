#!/bin/bash
set -e

date=$(date +"%F_%T")
out="b-$date.json"
FLEET_BENCH_TIMEOUT=${FLEET_BENCH_TIMEOUT-"5m"}
FLEET_BENCH_NAMESPACE=${FLEET_BENCH_NAMESPACE-"fleet-local"}

go run ./benchmarks/cmd run -d benchmarks/db -t "$FLEET_BENCH_TIMEOUT" -n "$FLEET_BENCH_NAMESPACE"
