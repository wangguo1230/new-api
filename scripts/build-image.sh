#!/usr/bin/env bash
#
# build-image.sh — 构建并推送生产 Docker 镜像
#
# 使用仓库根 Dockerfile（自包含多阶段：bun 构建前端 default+classic + Go 编译，
# CGO_ENABLED=0）。默认构建 linux/amd64 并推送 :latest 与版本 tag 到 GHCR。
#
# 可用环境变量：
#   IMAGE     镜像仓库名，默认 ghcr.io/wangguo1230/new-api
#   PLATFORM  目标平台，默认 linux/amd64
#   VERSION   版本号；默认 git describe，回退短 SHA
#   PUSH      1=构建后推送(默认)，0=仅本地构建不推送
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

IMAGE="${IMAGE:-ghcr.io/wangguo1230/new-api}"
PLATFORM="${PLATFORM:-linux/amd64}"
PUSH="${PUSH:-1}"

log()  { printf '\033[1;34m[image]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || die "未找到 docker。"

# ---- 版本号 ----------------------------------------------------------------
if [ -z "${VERSION:-}" ]; then
  VERSION="$(git describe --tags 2>/dev/null || true)"
  [ -z "$VERSION" ] && VERSION="v0.0.0-dev-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
fi

# Dockerfile 通过 `cat VERSION` 注入版本号；临时写入并在退出时还原，保持工作区干净。
restore_version() { git checkout -- VERSION 2>/dev/null || true; }
trap restore_version EXIT
printf '%s\n' "$VERSION" > VERSION

log "镜像:     $IMAGE"
log "平台:     $PLATFORM"
log "版本:     $VERSION"
log "推送:     $([ "$PUSH" = 1 ] && echo 是 || echo 否)"

# ---- 构建 ------------------------------------------------------------------
log "构建镜像中..."
docker build --platform "$PLATFORM" \
  -t "$IMAGE:latest" \
  -t "$IMAGE:$VERSION" \
  "$REPO_ROOT"

# ---- 推送 ------------------------------------------------------------------
if [ "$PUSH" = "1" ]; then
  log "推送 $IMAGE:$VERSION ..."
  if ! docker push "$IMAGE:$VERSION"; then
    die "推送失败。若为认证问题，请先登录：docker login ghcr.io"
  fi
  log "推送 $IMAGE:latest ..."
  docker push "$IMAGE:latest"
  log "完成 ✅ 已推送 :latest 与 :$VERSION 到 GHCR。"
else
  log "完成 ✅ 本地镜像已构建（未推送）：$IMAGE:$VERSION"
fi
