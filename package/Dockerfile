ARG BUILD_ENV=docker
ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG TARGETARCH
ARG ARCH=${TARGETARCH}

# renovate: datasource=github-release-attachments depName=krallin/tini
ARG TINI_VERSION=v0.19.0
# renovate: datasource=github-release-attachments depName=krallin/tini digestVersion=v0.19.0
ARG TINI_SUM_amd64=c5b0666b4cb676901f90dfcb37106783c5fe2077b04590973b885950611b30ee
# renovate: datasource=github-release-attachments depName=krallin/tini digestVersion=v0.19.0
ARG TINI_SUM_arm64=eae1d3aa50c48fb23b8cbdf4e369d0910dfc538566bfd09df89a774aa84a48b9

# Download both tini binaries on the native build platform to avoid QEMU networking
# issues when cross-building for arm64.
FROM --platform=$BUILDPLATFORM registry.suse.com/bci/bci-busybox:16.0 AS tini-downloader
ARG TINI_VERSION TINI_SUM_amd64 TINI_SUM_arm64
RUN set -eux; \
    wget -qO /tini-amd64 "https://github.com/krallin/tini/releases/download/${TINI_VERSION}/tini-static-amd64"; \
    echo "${TINI_SUM_amd64}  /tini-amd64" | sha256sum -c -; \
    wget -qO /tini-arm64 "https://github.com/krallin/tini/releases/download/${TINI_VERSION}/tini-static-arm64"; \
    echo "${TINI_SUM_arm64}  /tini-arm64" | sha256sum -c -; \
    chmod 0755 /tini-amd64 /tini-arm64

FROM --platform=linux/${ARCH} registry.suse.com/bci/bci-busybox:16.0 AS base
ARG ARCH
COPY --from=tini-downloader /tini-${ARCH} /usr/bin/tini
COPY package/log.sh /usr/bin/

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
ENTRYPOINT ["tini", "--"]
CMD ["fleetcontroller"]
