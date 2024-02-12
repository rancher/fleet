ARG BUILD_ENV=dapper
ARG ARCH

FROM --platform=linux/$ARCH registry.suse.com/bci/bci-busybox:15.5 AS base

FROM base AS copy_dapper
ONBUILD ARG ARCH
ONBUILD COPY bin/fleetcontroller-linux-${ARCH} /usr/bin/fleetcontroller

FROM base AS copy_buildx
ONBUILD ARG TARGETARCH
ONBUILD COPY bin/fleetcontroller-linux-${TARGETARCH} /usr/bin/fleetcontroller

FROM base AS copy_goreleaser
ONBUILD ARG ARCH
ONBUILD COPY fleetcontroller-linux-${ARCH} /usr/bin/fleetcontroller

FROM copy_${BUILD_ENV}
RUN addgroup -g 1000 fleet-apply && adduser -u 1000 -G fleet-apply -D fleet-apply
USER 1000
CMD ["fleetcontroller"]
