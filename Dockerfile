FROM --platform=$BUILDPLATFORM node:24.18.0-alpine AS web-builder

WORKDIR /build
RUN corepack enable \
    && corepack install --global pnpm@11.15.1

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/
RUN pnpm --dir web install --frozen-lockfile

COPY web ./web
RUN pnpm --dir web run build


FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS go-builder

ARG VERSION=2.0.0-dev
ARG TARGETOS
ARG TARGETARCH
ENV GO111MODULE=on \
    CGO_ENABLED=0

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY internal ./internal
COPY --from=web-builder /build/internal/webui/dist ./internal/webui/dist
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X gpt-load/internal/platform/version.Version=${VERSION}" \
    -o gpt-load


FROM alpine

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata \
    && update-ca-certificates \
    && addgroup -S -g 10001 gpt-load \
    && adduser -S -D -H -u 10001 -G gpt-load gpt-load \
    && mkdir -p /app/data \
    && chown 10001:10001 /app/data \
    && chmod 0700 /app/data

ENV DATA_DIR=/app/data
COPY --from=go-builder /build/gpt-load .
EXPOSE 3001
USER 10001:10001
ENTRYPOINT ["/app/gpt-load"]
