export GOARCH="${GOARCH:-amd64}"

docker build -f package/Dockerfile -t rancher/fleet:dev --build-arg="ARCH=$GOARCH"  .
docker build -f package/Dockerfile.agent -t rancher/fleet-agent:dev --build-arg="ARCH=$GOARCH" .
docker build -f package/Dockerfile.gitjob -t rancher/fleet-gitjob:dev --build-arg="ARCH=$GOARCH" .
