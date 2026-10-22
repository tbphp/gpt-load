FROM --platform=$BUILDPLATFORM node:22.23.4-alpine3.24 AS builder

ARG VERSION=1.0.0
WORKDIR /build
COPY ./web/package*.json ./
RUN npm ci
COPY ./web .
RUN VITE_VERSION=${VERSION} npm run build


FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine3.24 AS builder2

ARG VERSION=1.0.0
ARG TARGETOS
ARG TARGETARCH
ENV GO111MODULE=on \
    CGO_ENABLED=0

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /build/dist ./web/dist
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "-s -w -X gpt-load/internal/version.Version=${VERSION}" -o gpt-load


FROM alpine:3.24.3

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata \
    && update-ca-certificates

COPY --from=builder2 /build/gpt-load .
EXPOSE 3001
ENTRYPOINT ["/app/gpt-load"]
