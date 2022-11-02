FROM registry.suse.com/bci/bci-base:15.4.27.14.8
ARG ARCH
ENV ARCH=$ARCH
COPY bin/fleetcontroller-linux-${ARCH} /usr/bin/fleetcontroller
USER 1000
CMD ["fleetcontroller"]
