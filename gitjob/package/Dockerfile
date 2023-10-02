FROM registry.suse.com/bci/bci-base:15.5.36.5.37
RUN zypper -n update && \
    zypper -n install openssh catatonit && \
    zypper -n clean -a
RUN useradd -u 1000 -U -m gitjob
COPY bin/gitjob /usr/bin/
COPY bin/gitcloner /usr/bin
USER 1000
ENTRYPOINT ["catatonit", "--"]
CMD ["gitjob"]
