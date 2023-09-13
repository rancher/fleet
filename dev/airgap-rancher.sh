#!/bin/bash
set -e

# doesn't support windows

reg=${1-$priv}
ver=${2-2.7.0-rc8}

kubectl get pod -A  -o jsonpath='{range .items[*].spec.containers[*]}{.image}{"\n"}{end}' | sort -u | grep -v windows > short-list.txt

wget "https://github.com/rancher/rancher/releases/download/v$ver/rancher-images.txt" -O rancher-images.txt

essentials="rancher/kubectl
rancher/machine
rancher/pause
rancher/system-upgrade-controller
rancher/thanosio-thanos
rancher/webhook-receiver
rancher/rancher-webhook"

grep -f <(echo $essentials) rancher-images.txt | grep -v windows >> short-list.txt

function pullpush() {
  reg="$1"
  i="$2"

  grep "$i" rancher-images.txt

  if docker pull "$i" > /dev/null 2>&1; then
    echo "Image pull success: $i"
    pulled="$pulled $i"
  else
    if docker inspect "$i" > /dev/null 2>&1; then
      echo "Image exists: $i"
      pulled="$pulled $i"
    else
      echo "Image pull failed: $i"
    fi
  fi

  case $i in
    */*)
      image_name="$reg/$i"
      ;;
    *)
      image_name="$reg/rancher/$i"
      ;;
  esac

  docker tag "$i" "$image_name"
  docker push "$image_name"
}

while read -r i; do
  pullpush "$reg" "$i"
done < short-list.txt
