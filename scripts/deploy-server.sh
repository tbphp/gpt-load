#!/usr/bin/env bash
# Build gpt-load locally (or via buildx) and deploy directly to the server.
# Does not push to GHCR.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST="${DEPLOY_HOST:-root@179.253.233.109}"
SSH_KEY="${DEPLOY_SSH_KEY:-$HOME/.ssh/id_ed25519}"
SSH_OPTS=(-i "$SSH_KEY" -o StrictHostKeyChecking=accept-new -o HostKeyAlias=lax1.osid.cn)
IMAGE_NAME="${DEPLOY_IMAGE_NAME:-gpt-load:local}"
COMPOSE_DIR="${DEPLOY_COMPOSE_DIR:-/home/gpt-load}"
PLATFORM="${DEPLOY_PLATFORM:-linux/amd64}"
VERSION="${DEPLOY_VERSION:-$(git -C "$ROOT" rev-parse --short HEAD)}"

SSH=(ssh "${SSH_OPTS[@]}" "$HOST")
SCP=(scp "${SSH_OPTS[@]}")

log() { printf '==> %s\n' "$*"; }

log "version=$VERSION platform=$PLATFORM image=$IMAGE_NAME host=$HOST"

log "building image"
docker buildx build \
  --platform "$PLATFORM" \
  --build-arg "VERSION=$VERSION" \
  -t "$IMAGE_NAME" \
  --load \
  "$ROOT"

ARCHIVE="$(mktemp -t gpt-load-image.XXXXXX.tar)"
trap 'rm -f "$ARCHIVE"' EXIT

log "saving image to $ARCHIVE"
docker save "$IMAGE_NAME" -o "$ARCHIVE"

log "uploading image"
"${SCP[@]}" "$ARCHIVE" "$HOST:/tmp/gpt-load-image.tar"

log "loading image and recreating container"
"${SSH[@]}" bash -s <<REMOTE
set -euo pipefail
docker load -i /tmp/gpt-load-image.tar
rm -f /tmp/gpt-load-image.tar
cd "$COMPOSE_DIR"
# Point compose at the local image tag without editing the tracked file permanently:
# override via COMPOSE_IMAGE if compose uses that; otherwise rewrite image line temporarily.
if grep -q 'image:' docker-compose.yml; then
  sed -i.bak -E "s|^[[:space:]]*image:.*|    image: $IMAGE_NAME|" docker-compose.yml
fi
docker compose up -d --force-recreate --no-deps gpt-load
sleep 4
docker compose ps
docker inspect gpt-load --format 'Image={{.Config.Image}} Id={{.Image}} Status={{.State.Status}} Health={{if .State.Health}}{{.State.Health.Status}}{{end}} Restarts={{.RestartCount}}'
echo '---- logs ----'
docker logs --tail 30 gpt-load
REMOTE

log "deploy complete ($IMAGE_NAME / $VERSION)"
