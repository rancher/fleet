ARG BUILD_ENV=docker
ARG TARGETPLATFORM
ARG TARGETARCH
ARG ARCH=${TARGETARCH}

FROM --platform=linux/${ARCH} registry.suse.com/bci/bci-busybox:15.7 AS base
ARG ARCH
# Download tini-static v0.19.0 (stable final release) and verify via embedded SHA256 checksum.
RUN set -eux; \
    wget -qO /usr/bin/tini "https://github.com/krallin/tini/releases/download/v0.19.0/tini-static-${ARCH}"; \
    case "${ARCH}" in \
      amd64) echo "c5b0666b4cb676901f90dfcb37106783c5fe2077b04590973b885950611b30ee  /usr/bin/tini" | sha256sum -c - ;; \
      arm64) echo "eae1d3aa50c48fb23b8cbdf4e369d0910dfc538566bfd09df89a774aa84a48b9  /usr/bin/tini" | sha256sum -c - ;; \
      *) echo "Unsupported architecture: ${ARCH}" >&2; exit 1 ;; \
    esac; \
    chmod 0755 /usr/bin/tini
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
