ARG BUILD_ENV=bin

FROM registry.suse.com/bci/bci-base:15.4.27.11.29 AS base
USER 1000

FROM base AS copy_bin
ARG ARCH
ENV ARCH=$ARCH
ONBUILD COPY bin/fleetcontroller-linux-${ARCH} /usr/bin/fleetcontroller

FROM base AS copy_goreleaser
ONBUILD COPY fleetcontroller /usr/bin/fleetcontroller

FROM copy_${BUILD_ENV}
CMD ["fleetcontroller"]
