# Contributing to sitewarm

Thanks for helping. Bug reports, CDN header samples and pull requests are all
welcome.

## Before you start

- For anything larger than a small fix, open an issue first so we can agree on
  the approach before you write code.
- Security problems go to
  [private vulnerability reporting](https://github.com/clarivo-co/sitewarm/security/advisories/new),
  not to issues. See [SECURITY.md](SECURITY.md).
- By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development

You need the Go version in `go.mod`.

```
go build -o sitewarm ./cmd/sitewarm
gofmt -l .            # must print nothing
go vet ./...
go test -race ./...
```

Tests must not hit the network: use `httptest` servers and recorded headers.
Code that waits (retries, timeouts) is tested with `testing/synctest` so it
runs in virtual time.

### Adding or fixing a CDN

CDN detection lives in `internal/cdn`. Add a test with the real response
headers (redacted) for every cache status you map. Unmapped values must stay
`unknown`; do not guess.

## Pull requests

1. Fork, branch from `master`, keep the PR focused on one change.
2. Fill in the PR template. Update the README when flags, states, defaults or
   output change.
3. CI runs gofmt, vet and the race tests on Linux, macOS and Windows; all three
   must pass.
4. A maintainer reviews it. PRs are squash-merged, so the PR title becomes the
   commit message: write it as a short imperative sentence.

## Releasing (maintainers)

Releases are cut from `master` by pushing a version tag such as `v1.2.3` or
`v1.2.3-rc.1`. The `release` workflow runs the tests and then
[GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`), which builds
linux, darwin and windows for amd64 and arm64, packs them as `.tar.gz`
(`.zip` on Windows) with `checksums.txt` (SHA-256), and publishes a GitHub
release. A tag with a suffix after the patch number is published as a
pre-release. The version in the tag is embedded in `sitewarm --version` and
the User-Agent. Version tags are never moved or reused.

Check the config locally before changing it:

```
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```

Contributions are licensed under [Apache-2.0](LICENSE).
