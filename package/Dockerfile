ARG BUILD_ENV=docker
ARG TARGETPLATFORM
ARG TARGETARCH
ARG ARCH=${TARGETARCH}

FROM --platform=linux/${ARCH} registry.suse.com/bci/bci-nano:16.0 AS base
ARG ARCH

FROM base AS copy_docker
ONBUILD ARG ARCH
ONBUILD COPY bin/fleetcontroller-linux-$ARCH /usr/bin/fleetcontroller
ONBUILD COPY bin/fleet-linux-$ARCH /usr/bin/fleet

FROM base AS copy_buildx
ONBUILD ARG TARGETARCH
ONBUILD COPY bin/fleetcontroller-linux-$TARGETARCH /usr/bin/fleetcontroller
ONBUILD COPY bin/fleet-linux-$TARGETARCH /usr/bin/fleet

FROM base AS copy_goreleaser
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/fleetcontroller-linux-* /usr/bin/fleetcontroller
COPY ${TARGETPLATFORM}/fleet-linux-* /usr/bin/fleet

FROM copy_${BUILD_ENV}
USER 1000
CMD ["fleetcontroller"]
