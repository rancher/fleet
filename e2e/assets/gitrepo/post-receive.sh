#!/bin/sh

# This simulates a webhook call from Github, populating only fields which rancher/gitjob is known to care about [1].
# [1]: https://github.com/rancher/gitjob/blob/master/pkg/webhook/webhook.go#L129

# From https://stackoverflow.com/a/11150763
# (note: the branch last pushed to is not necessarily the checked out branch on the remote)
ref=$(find refs/heads -type f | sort | tail -1)
after=$(cat $ref)

# necessary to make rancher/gitjob interpret the call as a push event coming from Github
github_header="X-GitHub-Event: push"

curl \
    --retry-delay 5 \
    --retry 12 \
    --fail-with-body gitjob.cattle-fleet-system.svc.cluster.local \
    -H "$github_header" \
    -d "{\"ref\": \"$ref\", \"after\": \"$after\", \"repository\": {\"html_url\": \"{{.RepoURL}}\"}}"

echo "Webhook sent successfully"
