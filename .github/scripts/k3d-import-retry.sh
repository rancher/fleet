#!/bin/bash

set -ux

i=1
# "Tool" mode doesn't exit with 1 in case of an error, direct mode may freeze.
# Run tool mode and try to detect the error message in its output:
while k3d image import "$@" 2>&1 | tee out.$i | grep -iq "failed to import"; do
  cat out.$i
  i=$((i + 1))
  if (( i > 3 )); then
    echo "failed to import images"
    exit 1
  fi
  echo "retrying... $i"
  sleep 1
done
