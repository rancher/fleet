ARG BUILD_ENV=dapper
ARG ARCH

FROM --platform=linux/$ARCH registry.suse.com/bci/bci-base:15.5 AS base
RUN zypper in --no-recommends -y git-core openssh; rm -fr /var/cache/* /var/log/*log

FROM base AS copy_dapper
ONBUILD ARG ARCH
ONBUILD COPY bin/fleetcontroller-linux-$ARCH /usr/bin/fleetcontroller

FROM base AS copy_buildx
ONBUILD ARG TARGETARCH
ONBUILD COPY bin/fleetcontroller-linux-$TARGETARCH /usr/bin/fleetcontroller

FROM base AS copy_goreleaser
ONBUILD ARG ARCH
ONBUILD COPY fleetcontroller-linux-$ARCH /usr/bin/fleetcontroller

FROM copy_${BUILD_ENV}
USER 1000
CMD ["fleetcontroller"]
