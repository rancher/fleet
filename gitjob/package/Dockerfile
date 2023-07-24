FROM registry.suse.com/bci/bci-base:15.5.36.5.18
RUN zypper -n update && \
    zypper -n install git openssh catatonit && \
    zypper -n clean -a
RUN useradd -u 1000 -U -m gitjob
COPY bin/gitjob /usr/bin/
USER 1000
ENTRYPOINT ["catatonit", "--", "gitjob"]
