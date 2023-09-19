#!/bin/bash

set -euxo pipefail

i=0
# direct mode exits with 1 in case of an error, tool mode doesn't
while ! k3d image import "$@" -m direct; do
  i=$((i + 1))
  if (( i > 3 )); then
    echo "failed to import images"
    exit 1
  fi
  echo "retrying... $i"
  sleep 1
done
