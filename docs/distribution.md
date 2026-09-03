# Distribution and release maintenance

Packaging stays in this repository; no second hosted tap is required. These
recipes are ready for the next release. Adding them does not publish anything.
No public binary release has been published yet; use the
[source installation](personal-setup.md#install-the-current-checkout) now.

## Binary installer (after release publication)

Once a release with matching platform assets exists, install without Go, Node or
a compiler (Git is still needed for repositories):

```bash
diffmind_install_tmp=$(mktemp -d)
curl -fsSL https://raw.githubusercontent.com/mohammad-safakhou/diffmind/master/install.sh \
  -o "$diffmind_install_tmp/install.sh"
# Review the downloaded script before running it.
DIFFMIND_INSTALL_DIR="$HOME/.local/bin" sh "$diffmind_install_tmp/install.sh"
export PATH="$HOME/.local/bin:$PATH"
diffmind version --json
diffmind doctor
```

The installer checks the published archive checksum. Set `DIFFMIND_VERSION` to
an actual published version to pin it; an unset version selects latest. Until
assets exist, this command fails to download them. It does not install unpushed
local commits. Do not run the script with sudo; choose a user-writable directory.

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
   installs and tests each native archive, then publishes archives, checksums,
   and the pinned formula only after all platform gates pass.
4. Independently verify/download/install the assets on target platforms before
   promoting the formula or announcing the release.
5. Document backup compatibility and known limitations in release notes.

`make test-distribution` checks installer behavior, synthetic demo setup, formula
generation, and Ruby syntax. It does not publish or test all architectures.
Formula `test` blocks check version, doctor, and isolated backup/verify when a
maintainer runs `brew test`. Windows releases remain unsupported.

## Native installation gate

Every pull request/push runs native candidate checks on Linux amd64/arm64 and
macOS amd64/arm64. The release workflow runs the same check on its actual archive
before uploading it for publication. The native verifier serves release files
through a local `file://` source to the public installer, with a fresh private
workspace/install directory; no admin token, GitHub token or existing Diffmind
configuration is inherited. It checks the archive shape and executable version/
OS/architecture, installation doctor, SQLite migration and embedded UI assets.

CI uses the verifier's `--package-binary PATH` option to create a non-overwriting
archive first. The package contains exactly three regular files with neutral
numeric ownership and timestamps; it excludes macOS resource forks, xattrs and
local usernames/group names. Git commits and historical workspace timestamps are
not modified. The binary's embedded version/commit/build date supply provenance.

It then invokes both real-company acceptance tests with
`DIFFMIND_ACCEPTANCE_BINARY` pointing to that installed binary. These tests
exercise real Go/Python/Java extraction, HTTP/MCP queries, incremental reuse,
cancellation/retry history and managed backup rotation/restore on both queue
backends. This environment override is for trusted test binaries only.

To validate an already-built archive on its **matching native host**:

```bash
go run ./scripts/release-check \
  --archive /path/to/diffmind_1.2.3_darwin_arm64.tar.gz --version 1.2.3
# Or: make test-release-native ARCHIVE=/path/to/archive VERSION=1.2.3
```

The version/path above is illustrative; the file must be real and match the host.
The checker needs Go and the source checkout for its acceptance harness; users
installing the tested binary do not. It deletes only its own temporary check
directory on exit and never publishes or promotes a formula. A successful local
macOS check does not certify Linux or Intel; review all four CI job results.
This gate does not test Homebrew itself, distribution download availability,
code signing/notarization or all older Linux libc versions. Keep the independent
download/installation and formula-promotion checklist above.

Recipes follow the upstream [Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)
and [tap structure](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap).
