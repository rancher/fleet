#!/bin/bash

set -euxo pipefail

i=0
while k3d image import "$@" -m direct | grep -q "failed to import"; do
  i=$((i + 1))
  if (( i > 3 )); then
    echo "failed to import images"
    exit 1
  fi
  echo "retrying... $i"
  sleep 1
done
