FROM registry.suse.com/bci/bci-base:15.4.27.11.28
RUN zypper -n update && \
    zypper -n install git openssh && \
    zypper -n clean -a
RUN useradd -u 1000 -U -m gitjob
COPY bin/gitjob /usr/bin/
USER 1000
CMD ["gitjob"]
