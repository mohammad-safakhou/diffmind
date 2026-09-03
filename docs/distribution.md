# Distribution and release maintenance

Packaging stays in this repository; no second hosted tap is required. These
recipes are ready for the next release. Adding them does not publish anything.

## Homebrew development build

Once the formula is pushed, tap this repository explicitly:

```bash
brew tap diffmind/tap https://github.com/mohammad-safakhou/diffmind.git
brew install --HEAD diffmind/tap/diffmind
diffmind doctor
```

`Formula/diffmind.rb` is HEAD-only until a pinned recipe is promoted. It builds
`master` with Go and a C toolchain (tree-sitter uses CGO). Embedded web assets mean
Node is unnecessary. Git is a runtime dependency. It does not start services or
configure credentials. Update with `brew upgrade --fetch-HEAD diffmind/tap/diffmind`.

For a local checkout, use the absolute repository path as the tap URL and a
distinct local tap name. Homebrew requires formulae inside a tap; direct
`brew install ./Formula/diffmind.rb` is not the supported workflow here.

## Pinned release formula

Release CI generates a `diffmind.rb` asset from the four macOS/Linux amd64/arm64
archive checksums. Each platform gets an exact versioned URL and hash, never a
mutable `latest` URL. The formula itself is added to `checksums.txt`. Installing
these binaries needs no Go, Node, or compiler.

Maintainers can regenerate it:

```bash
go run ./scripts/release-formula --version 1.2.3 \
  --checksums /path/to/checksums.txt --output /path/to/new/diffmind.rb
```

`1.2.3` is illustrative, not a published-version claim. The generator rejects
missing/duplicate platform hashes, malformed versions, and existing output files.

After assets exist, review the generated recipe and promote it to
`Formula/diffmind.rb` in a normal tested PR to **this same repository**. Then
ordinary `brew install diffmind/tap/diffmind` becomes available. Promotion does
not automatically push to `master`. Retain a `head` block if both modes should
remain available. Alternatively users can put a verified pinned recipe in their
own local tap. Follow Homebrew's formula-trust prompts.

## Release checklist

1. Run `make verify`. Review target toolchain compatibility; Linux binaries inherit
   the runner's libc requirements and are not universal static binaries.
2. Commit embedded UI assets and ensure the tree/CI is clean.
3. A maintainer chooses and pushes a `vMAJOR.MINOR.PATCH` tag. CI verifies, builds,
   and publishes archives, checksums, and the pinned formula.
4. Independently verify/download/install the assets on target platforms before
   promoting the formula or announcing the release.
5. Document backup compatibility and known limitations in release notes.

`make test-distribution` checks installer behavior, synthetic demo setup, formula
generation, and Ruby syntax. It does not publish or test all architectures.
Formula `test` blocks check version, doctor, and isolated backup/verify when a
maintainer runs `brew test`. Windows releases remain unsupported.

Recipes follow the upstream [Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)
and [tap structure](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap).
