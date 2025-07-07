ARG BUILD_ENV=dapper
ARG ARCH

FROM --platform=linux/$ARCH registry.suse.com/bci/bci-base:15.6 AS base
COPY package/log.sh /usr/bin/
RUN zypper rm -y container-suseconnect && \
    zypper ar --priority=500 https://download.opensuse.org/repositories/Virtualization:containers/5.5/Virtualization:containers.repo && \
    zypper --gpg-auto-import-keys ref && \
    zypper -n update && \
    zypper -n install --no-recommends openssh-clients tini git-core && \
    zypper -n clean -a && \
    rm -fr /var/log/zypp* /usr/share/doc

FROM base AS copy_dapper
ONBUILD ARG ARCH
ONBUILD COPY bin/fleetcontroller-linux-$ARCH /usr/bin/fleetcontroller
ONBUILD COPY bin/fleet-linux-$ARCH /usr/bin/fleet

FROM base AS copy_buildx
ONBUILD ARG TARGETARCH
ONBUILD COPY bin/fleetcontroller-linux-$TARGETARCH /usr/bin/fleetcontroller
ONBUILD COPY bin/fleet-linux-$TARGETARCH /usr/bin/fleet

FROM base AS copy_goreleaser
ONBUILD ARG ARCH
ONBUILD COPY fleetcontroller-linux-$ARCH /usr/bin/fleetcontroller
ONBUILD COPY fleet-linux-$ARCH /usr/bin/fleet

FROM copy_${BUILD_ENV}
RUN useradd -u 1000 user
USER 1000
ENTRYPOINT ["tini", "--"]
CMD ["fleetcontroller"]
