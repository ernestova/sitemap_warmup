# sitemap_warmup

Checks and warms the CDN cache for every URL in a site's sitemap, and explains which URLs are not cacheable or not indexable.

## Language

**Origin**:
The site's own server that sits behind the CDN and answers when the CDN has no cached copy.
_Avoid_: Backend, source (and never use it for where requests come from)

**Vantage point**:
The place a scan's requests are sent from; the open-source CLI has exactly one (wherever you run it), the hosted service has one per region.
_Avoid_: Origin, node, probe location
