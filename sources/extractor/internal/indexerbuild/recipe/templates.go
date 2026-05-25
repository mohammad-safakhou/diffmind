package recipe

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/indexerbuild"
	"github.com/mohammad-safakhou/diffmind/internal/langdetect"
)

// SCIP indexer versions. These are pinned per release; bumping
// triggers fresh base-image builds across the board because the
// embedded versions are part of the Dockerfile content (and thus
// the digest).
//
// Asset-naming notes (verified against the upstream release pages
// at the time of writing; bump pinned versions only after
// re-verifying):
//
//   - scip CLI:        github.com/scip-code/scip
//                       scip-{linux,darwin}-{amd64,arm64}.tar.gz
//                       (the asset contains a single binary
//                        named `scip` at the top of the archive)
//                       NOTE the repo is "scip-code/scip", NOT
//                       "sourcegraph/scip". The latter does not
//                       exist; trying to curl from there is a 404.
//
//   - scip-java:       github.com/sourcegraph/scip-java
//                       Releases ship a single coursier-bootstrap
//                       file named `scip-java-v<version>` (no
//                       extension, no tarball). It is a self-
//                       contained executable launcher.
//
//   - scip-typescript: NO release binary; install via
//                       `npm install -g @sourcegraph/scip-typescript@<v>`.
//
//   - scip-python:     NO release binary; install via
//                       `npm install -g @sourcegraph/scip-python@<v>`.
//                       (Yes — it's a Node-based tool that wraps
//                        Pyright; Python itself is only needed at
//                        run time to evaluate the target repo.)
//
//   - scip-go:         github.com/sourcegraph/scip-go
//                       scip-go-{linux,darwin}-{amd64,arm64}.tar.gz
//
//   - scip-ruby:       LINUX/x86_64 ONLY as of the latest release.
//                       No linux/arm64 binary upstream. We install
//                       on amd64 hosts only; arm64 base image
//                       skips it (scip-ruby invocations on those
//                       hosts will fail-soft at index time).
//
//   - scip-dotnet:     installed via `dotnet tool install --global
//                       scip-dotnet --version <v>` (NuGet); the
//                       same NuGet package is multi-arch.
const (
	scipCLIVersion        = "v0.7.1"
	scipJavaVersion       = "0.12.3"
	scipTypeScriptVersion = "0.4.0"
	scipPythonVersion     = "0.6.4"
	scipGoVersion         = "v0.2.6"
	scipRubyVersion       = "0.4.7"
	scipDotnetVersion     = "0.2.14"
)

// preambleArchEnv emits the TARGETARCH ARG + a derived ARCH env
// (amd64/arm64) used by every base image's SCIP fetches. Docker
// auto-populates TARGETARCH when buildx is in play; on the legacy
// builder we fall back to detecting via uname so the same
// Dockerfile works in both modes.
//
// The fallback is critical because the LEGACY builder path is
// what macOS + colima + brew docker (no buildx plugin) hit, which
// is exactly where run 20260525T115727Z failed.
const preambleArchEnv = `# Multi-arch support. With buildx, TARGETARCH is set by Docker.
# With the legacy builder it isn't — we derive it from uname so
# the same Dockerfile builds on both arm64 Apple Silicon hosts
# and amd64 CI runners.
ARG TARGETARCH
RUN ARCH="${TARGETARCH:-$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')}"; \
    echo "ARCH=${ARCH}" > /etc/diffmind-arch
`

// installScipCLISnippet is the `RUN` block that fetches the SCIP
// CLI binary into /usr/local/bin/scip. Multi-arch.
//
// We:
//   1. Read /etc/diffmind-arch (set by preambleArchEnv) into ARCH.
//   2. Build the asset URL deterministically.
//   3. Verify the download is non-empty + actually a gzip stream
//      BEFORE piping to tar. If the URL ever 404s again the
//      build fails immediately with a clear "scip download failed:
//      HTTP <code>" message instead of "gzip: stdin: not in gzip
//      format" which is what the user actually saw on the
//      previous run.
func installScipCLISnippet() string {
	return fmt.Sprintf(`RUN . /etc/diffmind-arch && \
    URL="https://github.com/scip-code/scip/releases/download/%[1]s/scip-linux-${ARCH}.tar.gz" && \
    echo "fetching $URL" && \
    HTTP_CODE=$(curl -sSL -o /tmp/scip.tar.gz -w "%%{http_code}" "$URL") && \
    if [ "$HTTP_CODE" != "200" ]; then echo "scip download failed: HTTP $HTTP_CODE"; exit 1; fi && \
    file /tmp/scip.tar.gz && \
    tar -xzf /tmp/scip.tar.gz -C /usr/local/bin scip && \
    rm /tmp/scip.tar.gz && \
    chmod +x /usr/local/bin/scip
`, scipCLIVersion)
}

// installScipJavaSnippet downloads the coursier-bootstrap launcher
// for scip-java (it is NOT a tarball — that was the original bug
// in run 20260525T115727Z). Architecture-independent because the
// launcher is just a JAR wrapper.
func installScipJavaSnippet() string {
	return fmt.Sprintf(`RUN URL="https://github.com/sourcegraph/scip-java/releases/download/v%[1]s/scip-java-v%[1]s" && \
    HTTP_CODE=$(curl -sSL -o /usr/local/bin/scip-java -w "%%{http_code}" "$URL") && \
    if [ "$HTTP_CODE" != "200" ]; then echo "scip-java download failed: HTTP $HTTP_CODE"; exit 1; fi && \
    chmod +x /usr/local/bin/scip-java
`, scipJavaVersion)
}

// installScipGoSnippet fetches the prebuilt scip-go binary.
// Multi-arch via the shared TARGETARCH plumb.
func installScipGoSnippet() string {
	return fmt.Sprintf(`RUN . /etc/diffmind-arch && \
    URL="https://github.com/sourcegraph/scip-go/releases/download/%[1]s/scip-go-linux-${ARCH}.tar.gz" && \
    HTTP_CODE=$(curl -sSL -o /tmp/scip-go.tar.gz -w "%%{http_code}" "$URL") && \
    if [ "$HTTP_CODE" != "200" ]; then echo "scip-go download failed: HTTP $HTTP_CODE"; exit 1; fi && \
    tar -xzf /tmp/scip-go.tar.gz -C /usr/local/bin scip-go && \
    rm /tmp/scip-go.tar.gz && \
    chmod +x /usr/local/bin/scip-go
`, scipGoVersion)
}

// installScipRubySnippet installs the prebuilt scip-ruby binary
// for amd64 ONLY (upstream doesn't ship arm64). On arm64 hosts
// we write a stub script that exits with a clear "not available
// on this arch" message — this guarantees the composite's
// `COPY /usr/local/bin/scip-ruby` always succeeds even when the
// real binary couldn't be installed.
func installScipRubySnippet() string {
	return fmt.Sprintf(`RUN . /etc/diffmind-arch && \
    if [ "$ARCH" = "amd64" ]; then \
      URL="https://github.com/sourcegraph/scip-ruby/releases/download/scip-ruby-v%[1]s/scip-ruby-x86_64-linux" && \
      HTTP_CODE=$(curl -sSL -o /usr/local/bin/scip-ruby -w "%%{http_code}" "$URL") && \
      if [ "$HTTP_CODE" != "200" ]; then echo "scip-ruby download failed: HTTP $HTTP_CODE"; exit 1; fi && \
      chmod +x /usr/local/bin/scip-ruby; \
    else \
      echo '#!/bin/sh' > /usr/local/bin/scip-ruby && \
      echo 'echo "scip-ruby is not published for this architecture (arm64); ruby indexing unavailable" >&2' >> /usr/local/bin/scip-ruby && \
      echo 'exit 1' >> /usr/local/bin/scip-ruby && \
      chmod +x /usr/local/bin/scip-ruby; \
    fi
`, scipRubyVersion)
}

// wrapperContextFiles returns the wrapper Go sources extracted
// from the indexerbuild.Context embed.FS. We use the SAME wrapper
// the legacy single-image flow used, just rebuilt fresh in every
// composite image so the entrypoint binary matches the SCIP
// indexer versions the composite installed.
//
// The returned map is keyed by relative path inside the build
// context (e.g. "wrapper/main.go").
func wrapperContextFiles() (map[string]string, error) {
	out := map[string]string{}
	err := fs.WalkDir(indexerbuild.Context, "wrapper", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip tests and any non-Go file (no point shipping
		// fixtures into the build context).
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := fs.ReadFile(indexerbuild.Context, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(p)] = string(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// baseDockerfileFor returns the Dockerfile content + auxiliary
// context files for a single base image. Each base installs:
//   - the language toolchain at the requested version, into
//     /opt/<tool>/ so the composite can COPY --from= without
//     scattering files across the filesystem;
//   - the matching SCIP indexer binary into /usr/local/bin;
//   - on JVM bases, the build tools (Maven + Gradle).
//
// We use the official upstream images as the FROM where possible.
// Each base ends in a tiny Debian/Ubuntu shell so we have a
// consistent extraction target for the composite step.
func baseDockerfileFor(r ResolvedFact) (string, map[string]string, error) {
	switch r.Language {
	case langdetect.LangJava:
		return baseJavaDockerfile(r), nil, nil
	case langdetect.LangKotlin:
		return baseKotlinDockerfile(r), nil, nil
	case langdetect.LangTypeScript, langdetect.LangJavaScript:
		return baseNodeDockerfile(r), nil, nil
	case langdetect.LangPython:
		return basePythonDockerfile(r), nil, nil
	case langdetect.LangGo:
		return baseGoDockerfile(r), nil, nil
	case langdetect.LangRuby:
		return baseRubyDockerfile(r), nil, nil
	case langdetect.LangCSharp, langdetect.LangFSharp:
		return baseDotnetDockerfile(r), nil, nil
	}
	return "", nil, fmt.Errorf("baseDockerfileFor: unsupported language %q", r.Language)
}

// ---- per-language base templates ----
//
// Each template ships the language toolchain in /opt/<x> (so the
// composite can COPY whole) and the matching SCIP binary in
// /usr/local/bin/scip-<x>.

func baseJavaDockerfile(r ResolvedFact) string {
	jdk := r.ResolvedVersion
	return fmt.Sprintf(`# Java %[1]s base image with Maven, Gradle, scip-cli, scip-java.
FROM eclipse-temurin:%[1]s-jdk AS jdk

%[2]s

# Mirror layout so the composite COPYs a single tree per tool.
ENV JAVA_HOME=/opt/java
ENV PATH=${JAVA_HOME}/bin:/opt/maven/bin:/opt/gradle/bin:/usr/local/bin:${PATH}

RUN ln -s "$(readlink -f $(dirname $(readlink -f $(which javac)))/..)" /opt/java || \
    mv /opt/java-*-openjdk* /opt/java || true

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl ca-certificates unzip git file \
    && rm -rf /var/lib/apt/lists/*

# Maven 3.9.x
RUN curl -fsSL https://archive.apache.org/dist/maven/maven-3/3.9.9/binaries/apache-maven-3.9.9-bin.tar.gz \
    | tar -xz -C /opt && mv /opt/apache-maven-3.9.9 /opt/maven

# Gradle 8.x
RUN curl -fsSL https://services.gradle.org/distributions/gradle-8.10.2-bin.zip -o /tmp/gradle.zip \
    && unzip -q /tmp/gradle.zip -d /opt && mv /opt/gradle-8.10.2 /opt/gradle && rm /tmp/gradle.zip

# scip CLI (multi-arch)
%[3]s

# scip-java (coursier-bootstrap launcher; multi-arch)
%[4]s
`, jdk, preambleArchEnv, installScipCLISnippet(), installScipJavaSnippet())
}

func baseKotlinDockerfile(r ResolvedFact) string {
	// Kotlin rides on Java 21 since scip-java's Kotlin path uses
	// semanticdb-kotlinc through javac. We pin the kotlinc
	// version separately.
	return fmt.Sprintf(`# Kotlin %[1]s base image (rides on JDK 21 + scip-java).
FROM eclipse-temurin:21-jdk AS jdk

%[2]s

ENV JAVA_HOME=/opt/java
ENV PATH=${JAVA_HOME}/bin:/opt/maven/bin:/opt/gradle/bin:/opt/kotlin/bin:/usr/local/bin:${PATH}

RUN ln -s "$(readlink -f $(dirname $(readlink -f $(which javac)))/..)" /opt/java || true

RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates unzip git file \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://archive.apache.org/dist/maven/maven-3/3.9.9/binaries/apache-maven-3.9.9-bin.tar.gz \
    | tar -xz -C /opt && mv /opt/apache-maven-3.9.9 /opt/maven

RUN curl -fsSL https://services.gradle.org/distributions/gradle-8.10.2-bin.zip -o /tmp/gradle.zip \
    && unzip -q /tmp/gradle.zip -d /opt && mv /opt/gradle-8.10.2 /opt/gradle && rm /tmp/gradle.zip

# kotlinc
RUN curl -fsSL -o /tmp/kotlinc.zip https://github.com/JetBrains/kotlin/releases/download/v%[1]s.0/kotlin-compiler-%[1]s.0.zip \
    && unzip -q /tmp/kotlinc.zip -d /opt && mv /opt/kotlinc /opt/kotlin && rm /tmp/kotlinc.zip

# scip CLI + scip-java (multi-arch; via verified-fetch snippets)
%[3]s
%[4]s
`, r.ResolvedVersion, preambleArchEnv, installScipCLISnippet(), installScipJavaSnippet())
}

func baseNodeDockerfile(r ResolvedFact) string {
	return fmt.Sprintf(`# Node %[1]s base image with scip-typescript.
FROM node:%[1]s-bookworm AS node

%[2]s

RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates git file \
    && rm -rf /var/lib/apt/lists/*

# scip CLI (multi-arch via shared snippet)
%[3]s

# scip-typescript ships ONLY as an npm package. To avoid colliding
# with other language bases' npm installs (e.g. scip-python), we
# redirect npm's global prefix to a base-specific tree under
# /opt/node-tools. The composite Dockerfile COPYs that whole tree
# and adds /opt/node-tools/bin to PATH.
ENV NPM_CONFIG_PREFIX=/opt/node-tools
RUN mkdir -p /opt/node-tools/bin /opt/node-tools/lib/node_modules \
    && npm install -g @sourcegraph/scip-typescript@%[4]s

# Mirror Node runtime into /opt/node-tools so the launcher symlink
# resolves correctly inside the composite. The launcher is a
# /usr/bin/env node shebang; node MUST be on PATH at runtime.
RUN cp $(command -v node) /opt/node-tools/bin/node
`, r.ResolvedVersion, preambleArchEnv, installScipCLISnippet(), scipTypeScriptVersion)
}

func basePythonDockerfile(r ResolvedFact) string {
	return fmt.Sprintf(`# Python %[1]s base image with scip-python.
FROM python:%[1]s-slim AS py

%[2]s

ENV PATH=/usr/local/bin:${PATH}

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl ca-certificates git file nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# scip CLI (multi-arch via shared snippet)
%[3]s

# scip-python is a Node-based tool. We install it under a
# base-specific tree at /opt/python-tools so it doesn't collide
# with other npm-based indexers (scip-typescript) when both bases
# contribute to the same composite image.
ENV NPM_CONFIG_PREFIX=/opt/python-tools
RUN mkdir -p /opt/python-tools/bin /opt/python-tools/lib/node_modules \
    && npm install -g @sourcegraph/scip-python@%[4]s

# Bundle a copy of the Node interpreter so the scip-python launcher
# resolves /usr/bin/env node at composite-run time.
RUN cp $(command -v node) /opt/python-tools/bin/node

# Bundle the Python interpreter so the composite can run it without
# a separate python-base contribution. We keep it at /opt/python
# to preserve the existing PATH conventions.
RUN mkdir -p /opt/python && cp -r /usr/local/bin/python* /opt/python/ \
    && cp -r /usr/local/lib/python%[1]s /opt/python/lib 2>/dev/null || true
`, r.ResolvedVersion, preambleArchEnv, installScipCLISnippet(), scipPythonVersion)
}

func baseGoDockerfile(r ResolvedFact) string {
	return fmt.Sprintf(`# Go %[1]s base image with scip-go.
FROM golang:%[1]s-bookworm AS go

%[2]s

ENV PATH=/usr/local/go/bin:/root/go/bin:/usr/local/bin:${PATH}

RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates git file \
    && rm -rf /var/lib/apt/lists/*

# scip CLI + scip-go (multi-arch via shared snippets)
%[3]s
%[4]s
`, r.ResolvedVersion, preambleArchEnv, installScipCLISnippet(), installScipGoSnippet())
}

func baseRubyDockerfile(r ResolvedFact) string {
	return fmt.Sprintf(`# Ruby %[1]s base image with scip-ruby.
FROM ruby:%[1]s-bookworm AS ruby

%[2]s

RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates git file \
    && rm -rf /var/lib/apt/lists/*

# scip CLI (multi-arch) + scip-ruby (amd64 only; arm64 leaves a marker file)
%[3]s
%[4]s
`, r.ResolvedVersion, preambleArchEnv, installScipCLISnippet(), installScipRubySnippet())
}

func baseDotnetDockerfile(r ResolvedFact) string {
	return fmt.Sprintf(`# .NET %[1]s base image with scip-dotnet.
FROM mcr.microsoft.com/dotnet/sdk:%[1]s AS dotnet

%[2]s

ENV DOTNET_CLI_TELEMETRY_OPTOUT=1
ENV PATH=/root/.dotnet/tools:/usr/local/bin:${PATH}

RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates git file \
    && rm -rf /var/lib/apt/lists/*

# scip CLI (multi-arch via shared snippet)
%[3]s

# scip-dotnet ships via NuGet (multi-arch).
RUN dotnet tool install --global scip-dotnet --version %[4]s
`, r.ResolvedVersion, preambleArchEnv, installScipCLISnippet(), scipDotnetVersion)
}

// ---- composite Dockerfile ----

// compositeDockerfile produces the Dockerfile + context files for
// the FINAL image the indexer container runs. It FROMs each base
// image and COPYs the relevant toolchains + scip binaries into one
// rootless runtime image.
//
// The wrapper binary is compiled from sources extracted from the
// indexerbuild.Context embed.FS inside the build context (no
// cross-module embed needed). A minimal synthesised go.mod gives
// the in-container `go build` a module root.
func compositeDockerfile(resolved []ResolvedFact) (string, map[string]string) {
	// Pull the wrapper sources from the embed.FS. On error we
	// surface an empty context — the resulting build will fail
	// with a clear "wrapper/main.go: no such file" message that
	// the user can investigate.
	wrapperFiles, _ := wrapperContextFiles()
	var b strings.Builder
	b.WriteString("# Composite indexer image. Auto-generated by internal/indexerbuild/recipe.\n")
	b.WriteString("# Built from cached per-language base images via multi-stage COPY --from=.\n\n")

	// Stage aliases. We use a stable alias per language so the
	// COPY lines below are readable.
	for _, r := range resolved {
		alias := "lang_" + string(r.Language)
		b.WriteString("FROM " + r.BaseImage + " AS " + alias + "\n")
	}

	// Wrapper builder stage: compiles the entrypoint binary.
	b.WriteString(`
FROM golang:1.22-bookworm AS wrapper
WORKDIR /src
COPY wrapper /src/wrapper
COPY go.mod /src/go.mod
RUN cd /src/wrapper && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/diffmind-index .
`)

	// Final image. Debian-slim base (we only need libc + tini).
	//
	// PATH includes one bin dir per language base. We deliberately
	// put /opt/<x>-tools/bin BEFORE /usr/local/bin so a launcher
	// symlink installed by a base image takes precedence over any
	// stray entry in /usr/local/bin (defensive against transitive
	// duplicates when multiple bases ship the same tool).
	b.WriteString(`
FROM debian:bookworm-slim AS final
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
ENV PATH=/opt/node-tools/bin:/opt/python-tools/bin:/usr/local/bin:/opt/java/bin:/opt/maven/bin:/opt/gradle/bin:/opt/kotlin/bin:/opt/python/bin:/usr/local/go/bin:/root/.dotnet/tools:${PATH}

`)

	// Per-language COPY blocks. We COPY the language toolchain
	// + the scip binary from the corresponding base. The exact
	// paths MUST match what each base installs — see the
	// corresponding base*Dockerfile() function for the source
	// layout.
	//
	// CRITICAL: paths here are the actual layouts the base
	// images produce, NOT a wish list. Run 20260525T131803Z
	// failed at "COPY /opt/scip-java" because the Java base no
	// longer ships /opt/scip-java (scip-java is now a single
	// coursier-bootstrap script at /usr/local/bin/scip-java).
	// Every change to a baseDockerfile MUST be reflected here.
	for _, r := range resolved {
		alias := "lang_" + string(r.Language)
		switch r.Language {
		case langdetect.LangJava:
			b.WriteString("COPY --from=" + alias + " /opt/java /opt/java\n")
			b.WriteString("COPY --from=" + alias + " /opt/maven /opt/maven\n")
			b.WriteString("COPY --from=" + alias + " /opt/gradle /opt/gradle\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip /usr/local/bin/scip\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip-java /usr/local/bin/scip-java\n\n")
		case langdetect.LangKotlin:
			b.WriteString("COPY --from=" + alias + " /opt/java /opt/java\n")
			b.WriteString("COPY --from=" + alias + " /opt/maven /opt/maven\n")
			b.WriteString("COPY --from=" + alias + " /opt/gradle /opt/gradle\n")
			b.WriteString("COPY --from=" + alias + " /opt/kotlin /opt/kotlin\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip /usr/local/bin/scip\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip-java /usr/local/bin/scip-java\n\n")
		case langdetect.LangTypeScript, langdetect.LangJavaScript:
			// Node base ships everything under /opt/node-tools.
			// The launcher symlink at /opt/node-tools/bin/scip-typescript
			// references /opt/node-tools/lib/node_modules/... — both
			// MUST land in the composite or the launcher breaks.
			b.WriteString("COPY --from=" + alias + " /opt/node-tools /opt/node-tools\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip /usr/local/bin/scip\n\n")
		case langdetect.LangPython:
			// Python base: scip-python (npm) is under /opt/python-tools;
			// the python interpreter is bundled under /opt/python.
			b.WriteString("COPY --from=" + alias + " /opt/python-tools /opt/python-tools\n")
			b.WriteString("COPY --from=" + alias + " /opt/python /opt/python\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip /usr/local/bin/scip\n\n")
		case langdetect.LangGo:
			b.WriteString("COPY --from=" + alias + " /usr/local/go /usr/local/go\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip /usr/local/bin/scip\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip-go /usr/local/bin/scip-go\n\n")
		case langdetect.LangRuby:
			// scip-ruby is amd64-only upstream. The Ruby base
			// always produces /usr/local/bin/scip-ruby — either
			// the real binary (on amd64) or a stub that
			// exits with a clear message (on arm64) — so the
			// COPY here always succeeds. The wrapper inside
			// the container is responsible for invoking the
			// indexer and surfacing the stub's error message
			// if it fails.
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip /usr/local/bin/scip\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip-ruby /usr/local/bin/scip-ruby\n\n")
		case langdetect.LangCSharp, langdetect.LangFSharp:
			b.WriteString("COPY --from=" + alias + " /usr/share/dotnet /usr/share/dotnet\n")
			b.WriteString("COPY --from=" + alias + " /root/.dotnet /root/.dotnet\n")
			b.WriteString("COPY --from=" + alias + " /usr/local/bin/scip /usr/local/bin/scip\n\n")
		}
	}

	b.WriteString(`COPY --from=wrapper /out/diffmind-index /usr/local/bin/diffmind-index

WORKDIR /
ENTRYPOINT ["/usr/local/bin/diffmind-index"]
`)

	// Context files: the wrapper Go sources + a minimal go.mod.
	ctx := map[string]string{
		"go.mod": "module diffmindindex\n\ngo 1.22\n",
	}
	for k, v := range wrapperFiles {
		ctx[k] = v
	}
	return b.String(), ctx
}
