# sitemap_warmup

Checks and warms the CDN cache for every URL in a site's sitemap, and explains which URLs are not cacheable or not indexable.

## Language

**Origin**:
The site's own server that sits behind the CDN and answers when the CDN has no cached copy.
_Avoid_: Backend, source (and never use it for where requests come from)

**Vantage point**:
The place a scan's requests are sent from; the open-source CLI has exactly one (wherever you run it), the hosted service has one per region.
_Avoid_: Origin, node, probe location

**Verified site**:
A site whose owner has proven control of it (by DNS, file or meta tag); proving control of a root domain by DNS verifies every site under it. The hosted service only scans or warms verified sites.
_Avoid_: Verified domain, claimed site, registered domain

**Public check**:
A one-time, free scan of at most 50 URLs from an unverified site, offered in exchange for an email address.
_Avoid_: Trial, free scan, demo

**Site**:
One hostname (the apex and its `www` count as one) with one or more sitemaps; the unit a customer pays for.
_Avoid_: Project, property, workspace, domain

**Organization**:
The customer account that owns sites, their verification and the subscription; it has one or more members, and every user starts with a personal one.
_Avoid_: Account, team, workspace, tenant

**Cache status**:
What the CDN reports for one URL at one vantage point: hit, miss, or unknown when the CDN hides it even after asking for its debug headers.
_Avoid_: Cache state, cache result
