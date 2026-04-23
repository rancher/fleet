# AI Contribution Policy (provisional)

Fleet is GitOps and HelmOps at scale. Fleet is designed to manage
multiple clusters. It's also lightweight enough that it works great for
a single cluster too, but it really shines when you get to a large
scale. By large scale we mean either a lot of clusters, a lot of
deployments, or a lot of teams in a single organization.

This policy sets expectations for AI-assisted contributions so
contributors and maintainers can collaborate effectively.

It is provisional and might be replaced by a Rancher-wide policy sometime in the future.

Fleet maintainers and contributors use AI tools in their own workflows,
and we have no objection to contributors doing the same. AI can help you
understand unfamiliar code, draft documentation, generate test cases, or
explore implementation options. We want contributors who use AI as a
productivity aid, not as a replacement for understanding.

**AI tools are welcome in the Fleet contributor workflow. But the human
contributor is always accountable.**

## Contribution Guidelines

The following rules apply to every contribution, regardless of how it was produced:

- **Understand your changes.** You must be able to explain every change you
  submit. "The AI generated it" is never an acceptable answer.
- **Design first.** For non-trivial changes, open an Issue before a PR. PRs
  that don't align with our architectural patterns will be closed.
- **Quality over quantity.** One thoughtful PR is worth more than many
  AI-assisted fixes for trivial issues.
- **Legal compliance.** Fleet requests you to provide a [Developer
  Certificate of Origin](https://github.com/apps/dco) (DCO) with your contribution.
  It's the contributor's responsibility to understand and accept the
  legal consequences of such a DCO.

## Disclosure

If AI was used to generate any part of your contribution, disclose
it in the PR description and add an `Assisted-by:` trailer to the relevant
commits:

```
Assisted-by: GitHub Copilot
Assisted-by: Claude Opus 4.5
Assisted-by: ChatGPT 5.2
```

## Engaging With Maintainers

Contributing is a collaborative process. When maintainers leave feedback, follow these rules:

- **Respond personally.** Do not feed review feedback back into an AI and
  blindly apply the output. Your responses must reflect genuine understanding.
- **No AI ping-pong.** If maintainers observe a pattern of AI-driven responses
  without real engagement, the PR will be closed.
- Maintainers reserve the right to close any low-effort AI contribution without
  a detailed technical critique.

This is not an anti-AI stance. It is a stance for quality, openness, traceability, and sustainability.

## Acknowledgement

This policy was derived from [Kubewarden](https://www.kubewarden.io),
see [their Acknowledgement section](https://github.com/kubewarden/community/blob/main/AI_POLICY.md#acknowledgement) for further details.
