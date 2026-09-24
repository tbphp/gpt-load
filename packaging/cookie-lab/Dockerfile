FROM alpine:3.24.1

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 gpt-load \
    && adduser -S -D -H -u 10001 -G gpt-load gpt-load \
    && mkdir -p /app/data \
    && chown 10001:10001 /app/data \
    && chmod 0700 /app/data

COPY --chmod=0755 gpt-load /app/gpt-load

WORKDIR /app
ENV HOST=0.0.0.0
ENV DATA_DIR=/app/data
EXPOSE 3001
USER 10001:10001
ENTRYPOINT ["/app/gpt-load"]
