ARG BUILD_ENV=dapper
ARG ARCH

FROM --platform=linux/$ARCH registry.suse.com/bci/bci-base:15.5 AS base
COPY package/log.sh /usr/bin/
RUN zypper -n update && \
    zypper -n install openssh-clients catatonit git-core && \
    zypper -n clean -a

FROM base AS copy_dapper
ONBUILD ARG ARCH
ONBUILD COPY bin/fleetcontroller-linux-$ARCH /usr/bin/fleetcontroller
ONBUILD COPY bin/fleet-linux-$ARCH /usr/bin/fleet
ONBUILD COPY bin/gitjob-linux-$ARCH /usr/bin/gitjob
ONBUILD COPY bin/gitcloner-linux-$ARCH /usr/bin/gitcloner

FROM base AS copy_buildx
ONBUILD ARG TARGETARCH
ONBUILD COPY bin/fleetcontroller-linux-$TARGETARCH /usr/bin/fleetcontroller
ONBUILD COPY bin/fleet-linux-$TARGETARCH /usr/bin/fleet
ONBUILD COPY bin/gitjob-linux-$TARGETARCH /usr/bin/gitjob
ONBUILD COPY bin/gitcloner-linux-$TARGETARCH /usr/bin/gitcloner

FROM base AS copy_goreleaser
ONBUILD ARG ARCH
ONBUILD COPY fleetcontroller-linux-$ARCH /usr/bin/fleetcontroller
ONBUILD COPY fleet-linux-$ARCH /usr/bin/fleet
ONBUILD COPY gitjob-linux-$ARCH /usr/bin/gitjob
ONBUILD COPY gitcloner-linux-$ARCH /usr/bin/gitcloner

FROM copy_${BUILD_ENV}
USER 1000
ENTRYPOINT ["catatonit", "--"]
CMD ["fleetcontroller"]
