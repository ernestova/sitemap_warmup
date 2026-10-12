# Verify domain ownership before any hosted scan or warm

The hosted service only scans or warms verified domains, even though a read-only scan looks harmless and an agency may find it inconvenient to get DNS access to a client's site. Sending traffic from many regional vantage points at a domain we don't know the owner of makes us a tool for load attacks and competitor scraping, and gets our egress IPs blocked by WAFs. The single exception is the public check: one free run of at most 50 URLs, to bring in new users.
