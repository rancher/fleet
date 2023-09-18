#!/bin/bash

set -euxo pipefail

container=${1:-k3d-upstream}
name=${2:-fleet}
shift
shift

echo "Run k3d import with \"$*\" and retry if '$name' is not found in 'critcl images' output"

i=0
while ! ( docker exec "$container"-server-0 /bin/crictl images | grep -q "$name" ); do
  i=$((i + 1))
  if (( i > 3 )); then
    echo "failed to import images"
    exit 1
  fi
  k3d image import "$@"
  # crictl images doesn't show the image immediately after import
  sleep 20
done
