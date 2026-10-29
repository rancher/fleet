#!/bin/bash

set -ux

# k3d "tools" mode reports success even when a node failed to import (e.g.
# its containerd was not ready yet), and "direct" mode can hang. Missing images
# leave fleet pods in ImagePullBackOff, so verify presence on every node.

sock=unix:///run/k3s/containerd/containerd.sock
max_attempts=${MAX_ATTEMPTS:-5}

cluster=""
images=()
while [ $# -gt 0 ]; do
  case "$1" in
  -c | --cluster)
    cluster=$2
    shift 2
    ;;
  *)
    # Skip images this job did not build, e.g. nginx-git:test.
    docker image inspect "$1" >/dev/null 2>&1 && images+=("$1")
    shift
    ;;
  esac
done

nodes=$(docker ps -a --filter "label=k3d.cluster=$cluster" \
  --format '{{.Label "k3d.role"}} {{.Names}}' |
  awk '$1 == "server" || $1 == "agent" { print $2 }')
[ -n "$nodes" ] || { echo "cluster $cluster has no nodes"; exit 1; }

all_present() {
  local node
  for node in $nodes; do
    docker exec "$node" crictl -r "$sock" inspecti -q "${images[@]}" >/dev/null || return 1
  done
}

for attempt in $(seq "$max_attempts"); do
  k3d image import "${images[@]}" -c "$cluster"
  all_present && exit 0
  echo "import attempt $attempt of $max_attempts incomplete"
  [ "$attempt" -lt "$max_attempts" ] && sleep $((attempt * 5))
done

exit 1
