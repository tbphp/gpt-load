FROM --platform=$BUILDPLATFORM node:24.18.0-alpine3.24@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS web-builder

WORKDIR /build
RUN corepack enable \
    && corepack install --global pnpm@11.17.0

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/
RUN pnpm --dir web install --frozen-lockfile

COPY internal/webui/page_routes.json ./internal/webui/page_routes.json
COPY web ./web
RUN pnpm --dir web run build


FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine3.24@sha256:af8d6740070b8906d12eae1c3e3ea0957fb63f492051ea05e354c38ef9fe88df AS go-builder

ARG VERSION=2.0.0-dev
ARG TARGETOS
ARG TARGETARCH
ENV GO111MODULE=on \
    CGO_ENABLED=0

WORKDIR /build

COPY go.mod go.sum ./
COPY third_party/cpaembedded/go.mod third_party/cpaembedded/go.sum ./third_party/cpaembedded/
RUN go mod download

COPY main.go ./
COPY internal ./internal
COPY third_party/cpaembedded ./third_party/cpaembedded
COPY --from=web-builder /build/internal/webui/dist ./internal/webui/dist
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X gpt-load/internal/platform/version.Version=${VERSION}" \
    -o gpt-load


FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata \
    && update-ca-certificates \
    && addgroup -S -g 10001 gpt-load \
    && adduser -S -D -H -u 10001 -G gpt-load gpt-load \
    && mkdir -p /app/data \
    && chown 10001:10001 /app/data \
    && chmod 0700 /app/data

ENV HOST=0.0.0.0
ENV DATA_DIR=/app/data
COPY --from=go-builder /build/gpt-load .
COPY LICENSE THIRD_PARTY_NOTICES.md /app/licenses/
COPY LICENSES/Apache-2.0.txt /app/licenses/Apache-2.0.txt
COPY LICENSES/MIT.txt /app/licenses/MIT.txt
EXPOSE 3001 1455 54545 51121
USER 10001:10001
ENTRYPOINT ["/app/gpt-load"]
