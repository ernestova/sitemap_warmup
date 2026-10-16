[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fernestova%2Fsitemap_warmup.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Fernestova%2Fsitemap_warmup?ref=badge_shield)

# sitewarm

Warms your top pages through the CDN after a deploy and verifies, per URL and
browser profile, that the CDN now serves them from cache.

> **Work in progress.** This branch is the Go rewrite. The original Python
> sitemap crawler lives in [`legacy/`](legacy/) and is frozen at tag
> [`v0-python`](https://github.com/ernestova/sitemap_warmup/tree/v0-python).

## Build

```
go build -o sitewarm ./cmd/sitewarm
```

## Usage

```
sitewarm scan --top-from top.csv
```

`top.csv` has a header row with a `url` column (absolute URLs) or a `path`
column (resolved with `--base-url`). Rows are in priority order; `--top N`
keeps the first N (default 50).

Each URL is requested with two profiles, `desktop` and `mobile`, that send the
navigation headers of current Chrome stable (Windows and Android) plus a
`sitewarm/<version>` token in the User-Agent. sitewarm never impersonates a
crawler. Requests are spread with `--concurrency 4` and a global `--rps 5`
that also covers retries. A `miss` or `stale` URL is requested again every
`--retry-interval 10s` until it is a `hit` or `--max-wait 5m` passes. The whole
run stops at `--run-timeout 15m`.

| Flag | Default | |
|---|---|---|
| `--top-from` | | CSV with the URLs |
| `--base-url` | | base for a `path` column |
| `--top` | 50 | URLs to warm |
| `--concurrency` | 4 | requests in flight |
| `--rps` | 5 | global requests per second |
| `--retry-interval` | 10s | wait between attempts |
| `--max-wait` | 5m | retry window per URL (`0` = one request) |
| `--run-timeout` | 15m | cap for the whole run |
| `--fail-on` | `miss,client-error,error,incomplete,noindex,redirect-error` | states that fail the run |
| `--accept-language` | `en-US,en;q=0.9` | for both profiles |
| `--cookie-hosts` | | hosts that may receive cookies |
| `--format` | `table` | stdout format: `table` or `json` |
| `--report` | | also write the JSON report to a file |

### States

| State | Meaning |
|---|---|
| `hit` | served from the edge cache |
| `stale` | served from cache but expired |
| `miss` | not in cache |
| `bypass` | the CDN does not cache it (BYPASS, DYNAMIC, or a miss with `private`/`no-store`) |
| `variant-unverifiable` | the response has `Vary: User-Agent` |
| `challenged` | 403 or a bot challenge |
| `client-error` | another 4xx (404, 410...) |
| `error` | 5xx, timeout or network error, after 3 retries |
| `redirect-error` | redirect loop, more than 5 hops, or a hop to another host |
| `incomplete` | not finished before `--run-timeout` or an interrupt |
| `unknown` | the CDN does not expose a cache status |

Supported CDNs: Cloudflare (`cf-cache-status`), Vercel (`x-vercel-cache`),
CloudFront, Fastly (the last value of `x-cache`, the edge that answered) and
Akamai (debug headers requested with `Pragma`). Unmapped values are `unknown`
and the raw value is kept. When several CDNs are stacked, the outermost one
decides.

Redirects are followed up to 5 hops on the same host (apex and `www` count as
one) and the final URL is verified. Bodies are decoded (gzip, deflate, br,
zstd) and the first 256 KB of HTML are checked for `noindex`; the rest is
downloaded and discarded so the CDN finishes caching it. Each request has a
30 s deadline.

### Cookies

Cookies come only from `SITEWARM_COOKIE_DESKTOP` / `SITEWARM_COOKIE_MOBILE`,
are sent only to `--cookie-hosts` and only over HTTPS. A cookie without
`--cookie-hosts` is an error at startup.

### Exit codes

`0` passed, `1` some result is in `--fail-on`, `2` invalid usage or
configuration, `130`/`143` interrupted by SIGINT/SIGTERM. An interrupt or the
run timeout still writes the report, with unfinished URLs as `incomplete`;
a second Ctrl-C exits at once.

### Limitations

A `hit` is verified only at the CDN PoP serving the machine that runs
sitewarm. A cache key that depends on device or User-Agent without saying so
in `Vary` can make a `hit` unrepresentative of real users.

## License

Apache-2.0. See [LICENSE](LICENSE).
