FROM registry.suse.com/bci/bci-base:15.3.17.17.8
RUN zypper -n update && \
    zypper -n install git openssh && \
    zypper -n clean -a
RUN useradd -u 1000 -U -m gitjob
COPY bin/gitjob /usr/bin/
USER 1000
CMD ["gitjob"]
