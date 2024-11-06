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

n=$(kubectl get clusters.fleet.cattle.io -n "$FLEET_BENCH_NAMESPACE" -l fleet.cattle.io/benchmark=true  -ojson | jq '.items | length')
if [ "$n" -eq 0 ]; then
  echo "No clusters found to benchmark"
  echo "You need at least one cluster with the label fleet.cattle.io/benchmark=true"
  echo
  echo "Example:"
  echo "kubectl label clusters.fleet.cattle.io -n fleet-local local fleet.cattle.io/benchmark=true"
  exit 1
fi

ginkgo run --fail-fast --seed 1731598958 --json-report "$FLEET_BENCH_OUTPUT" ./benchmarks
