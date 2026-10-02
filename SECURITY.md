# Security Policy

## Supported versions

infrastructure ships several independently versioned components (`infra-api`,
`infra-controller-manager`, `infra-agent`, `infra`, `terraform-provider-infra`,
`infra-idp`, `infra-exporter`, `infra-container-init`). Security fixes are
applied only to the latest released minor of each component.

| Component | Supported |
| --- | --- |
| Each component - latest minor | yes |
| Older minors or pre-releases | no |

## Reporting a vulnerability

**Please do not open a public issue.** GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
is the preferred channel:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Fill in the form with the affected component, version, reproduction steps and
   suggested mitigation.

If you cannot use GitHub's form, email **goabonga@pm.me** with the same
information. PGP encryption is available on request.

You can expect:

- an acknowledgement within **3 business days**;
- a triage assessment (severity, scope, affected components) within **10
  business days**;
- a fix or written mitigation plan before any public disclosure.

## Disclosure process

Coordinated disclosure is the default. Once a fix is released:

1. A patched version is published for each affected component (GitHub Releases).
2. A GitHub Security Advisory is opened with a CVE when applicable.
3. The reporter is credited in the advisory unless they request anonymity.

Thanks for helping keep infrastructure and its users safe.
