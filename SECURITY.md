# Security policy

## Supported versions

Calnode is pre-1.0, and fixes go onto the latest release line only. There are no
backports to earlier `0.x` minors.

| Version | Supported |
| ------- | --------- |
| 0.9.x   | Yes       |
| < 0.9   | No        |

If you are running an older tag, the fix for a reported issue will be an upgrade.

## Reporting a vulnerability

Please report privately, through GitHub's private vulnerability reporting: open the
repository's **Security** tab and use **Report a vulnerability**. That opens a private
thread visible only to you and the maintainers.

Please do not open a public issue, a pull request, or a discussion for a security
report. A public issue is a disclosure, and it is one made before there is anything for
people to upgrade to.

## What to include

Enough to reproduce it. Usually that is:

- what the problem is, and what an attacker gets out of it;
- the version, tag, or commit you tested;
- how you are running it (Docker image, `go build`, behind which proxy) and anything
  non-default in your configuration;
- the steps, request, or proof-of-concept that triggers it;
- what you expected to happen instead.

If you are not sure whether something is a vulnerability, report it anyway and say so.
An unclear report is easier to deal with than one that never arrives.

## What to expect

- **An acknowledgement within a few days.** If you have not heard anything after a week,
  please post a follow-up on the same private thread in case it was missed.
- **Then either a fix or an explanation.** If we agree it is a vulnerability, we will
  tell you roughly when a fix will land and let you know when it ships. If we do not
  think it is one, we will say why rather than leaving the report open.
- **A public advisory when the fix is released**, through GitHub Security Advisories, so
  people running Calnode know what they are upgrading for.
- **Credit, if you want it.** Tell us the name or handle to use, or tell us you would
  rather stay anonymous. Either is fine.

Please give us a reasonable chance to ship a fix before publishing the details.
