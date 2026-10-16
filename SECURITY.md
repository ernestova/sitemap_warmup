# Security policy

## Reporting a vulnerability

Please do not open a public issue for security problems.

Report them privately through GitHub:
[Security → Report a vulnerability](https://github.com/clarivo-co/sitewarm/security/advisories/new).
Include the version (`sitewarm --version`), what an attacker can do, and the
steps to reproduce.

You should get an acknowledgement within 7 days. We will keep you updated
while we work on a fix and credit you in the advisory unless you ask us not to.

## Supported versions

Only the latest release gets security fixes.

## Scope

sitewarm sends requests to sites you point it at, and can send cookies from
`SITEWARM_COOKIE_DESKTOP` / `SITEWARM_COOKIE_MOBILE`. Issues we especially want
to hear about:

- cookies or other secrets sent to a host outside `--cookie-hosts`, over plain
  HTTP, or written to the report or logs;
- a response from a target site that crashes sitewarm or makes it use unbounded
  memory, disk or time;
- redirects that escape the host checks.
