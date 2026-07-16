FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

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
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "-s -w -X gpt-load/internal/platform/version.Version=${VERSION}" -o gpt-load


FROM alpine

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata \
    && update-ca-certificates

COPY --from=builder /build/gpt-load .
EXPOSE 3001
ENTRYPOINT ["/app/gpt-load"]
