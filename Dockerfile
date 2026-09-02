# syntax=docker/dockerfile:1.7

FROM node:24-bookworm-slim AS web
WORKDIR /src

COPY internal/workspace/ui/web/package*.json internal/workspace/ui/web/
RUN npm --prefix internal/workspace/ui/web ci
COPY internal/workspace/ui/web internal/workspace/ui/web
RUN npm --prefix internal/workspace/ui/web run build

COPY internal/extractor/ui/web/package*.json internal/extractor/ui/web/
RUN npm --prefix internal/extractor/ui/web ci
COPY internal/extractor/ui/web internal/extractor/ui/web
RUN npm --prefix internal/extractor/ui/web run build

FROM golang:1.26.6-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/workspace/ui/web/dist internal/workspace/ui/web/dist
COPY --from=web /src/internal/extractor/ui/web/dist internal/extractor/ui/web/dist

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/diffmind ./cmd/diffmind

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /data \
    && chown 65532:65532 /data

COPY --from=build /out/diffmind /usr/local/bin/diffmind

ENV HOME=/data \
    DIFFMIND_HOME=/data \
    DIFFMIND_BINARY=/usr/local/bin/diffmind

VOLUME ["/data"]
EXPOSE 8090
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl --fail --silent --show-error http://127.0.0.1:8090/healthz >/dev/null || exit 1

ENTRYPOINT ["diffmind"]
CMD ["ui", "--host", "0.0.0.0", "--port", "8090", "--no-spa-rebuild"]
