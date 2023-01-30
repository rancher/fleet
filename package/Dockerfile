ARG BUILD_ENV=dapper

FROM registry.suse.com/bci/bci-base:15.4.27.14.31 AS base
USER 1000

FROM base AS copy_dapper
ONBUILD ARG ARCH
ONBUILD COPY bin/fleetcontroller-linux-${ARCH} /usr/bin/fleetcontroller

FROM base AS copy_buildx
ONBUILD ARG TARGETARCH
ONBUILD COPY bin/fleetcontroller-linux-${TARGETARCH} /usr/bin/fleetcontroller

FROM copy_${BUILD_ENV}
CMD ["fleetcontroller"]
