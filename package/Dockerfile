FROM registry.suse.com/bci/bci-base:15.4.27.8.1
ARG ARCH
ENV ARCH=$ARCH
COPY bin/fleetcontroller-linux-${ARCH} /usr/bin/fleetcontroller
RUN useradd -m -U fleet
USER fleet
CMD ["fleetcontroller"]
