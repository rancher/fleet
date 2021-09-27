FROM alpine:3.14.2
RUN apk -U --no-cache add git openssh && adduser -u 1000 -D gituser
COPY bin/gitjob /usr/bin/
USER 1000
CMD ["gitjob"]
