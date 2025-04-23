#!/bin/bash
set -e

date=$(date +"%F_%T")
out="b-$date.json"
FLEET_BENCH_OUTPUT=${FLEET_BENCH_OUTPUT-$out}
FLEET_BENCH_TIMEOUT=${FLEET_BENCH_TIMEOUT-"5m"}
FLEET_BENCH_NAMESPACE=${FLEET_BENCH_NAMESPACE-"fleet-local"}
FLEET_BENCH_METRICS=${FLEET_BENCH_METRICS-"true"}

export FLEET_BENCH_TIMEOUT
export FLEET_BENCH_NAMESPACE
export FLEET_BENCH_METRICS

go run ./benchmarks/cmd run -d benchmarks/db -i "$FLEET_BENCH_OUTPUT"
