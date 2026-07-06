#!/usr/bin/env bash
#
# build-release.sh — 本地完整发布产物构建
#
# 复刻原 GitHub release 流水线（前端 bun 构建 + 后端 go 交叉编译 + 校验和），
# 供本地 pre-push hook 或手动调用。因 SQLite 使用纯 Go 驱动 glebarez/sqlite，
# 全程 CGO_ENABLED=0，无需 cross gcc 即可交叉编译全平台。
#
# 可用环境变量：
#   BUILD_TARGETS   目标平台列表（空格分隔 "os/arch"），默认全平台矩阵
#   OUTPUT_DIR      产物输出目录，默认 <repo>/build
#   GO_BIN          指定 go 可执行文件；默认自动探测 >=1.22 的 go
#   BUN_BIN         指定 bun 可执行文件；默认探测 PATH 与 ~/.bun/bin/bun
#   VERSION         版本号；默认 git describe，回退 VERSION 文件 / v0.0.0-dev
#   SKIP_FRONTEND   非空则跳过前端构建（复用已有 dist）
#
set -euo pipefail

# ---- 定位仓库根 -------------------------------------------------------------
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

MODULE_PATH="github.com/QuantumNous/new-api"
OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/build}"
# 默认仅构建 amd64（服务器部署目标）。需要其它平台时用 BUILD_TARGETS 覆盖，
# 例如：BUILD_TARGETS="linux/amd64 linux/arm64 darwin/arm64" ...
DEFAULT_TARGETS="linux/amd64"
BUILD_TARGETS="${BUILD_TARGETS:-$DEFAULT_TARGETS}"

log()  { printf '\033[1;34m[build]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n'  "$*"; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

# ---- 解析版本号 -------------------------------------------------------------
resolve_version() {
  if [ -n "${VERSION:-}" ]; then echo "$VERSION"; return; fi
  local v
  v="$(git describe --tags 2>/dev/null || true)"
  if [ -z "$v" ] && [ -s VERSION ]; then v="$(tr -d '[:space:]' < VERSION)"; fi
  [ -z "$v" ] && v="v0.0.0-dev-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
  echo "$v"
}

# ---- 探测 go (>=1.22) -------------------------------------------------------
go_ge_122() {
  # 输入形如 go1.20.2 / go1.25.11，判断 major.minor >= 1.22
  local ver="${1#go}"; local major="${ver%%.*}"; local rest="${ver#*.}"; local minor="${rest%%.*}"
  [ "$major" -gt 1 ] 2>/dev/null && return 0
  [ "$major" -eq 1 ] 2>/dev/null && [ "$minor" -ge 22 ] 2>/dev/null && return 0
  return 1
}

resolve_go() {
  if [ -n "${GO_BIN:-}" ]; then echo "$GO_BIN"; return; fi
  local cand raw
  for cand in go "$HOME/sdk/go1.25.11/bin/go" /usr/local/go/bin/go; do
    if command -v "$cand" >/dev/null 2>&1 || [ -x "$cand" ]; then
      raw="$("$cand" version 2>/dev/null | awk '{print $3}')" || continue
      if go_ge_122 "$raw"; then echo "$cand"; return; fi
    fi
  done
  die "未找到 go>=1.22（系统 go 过旧）。请安装或设置 GO_BIN，例如 GO_BIN=~/sdk/go1.25.11/bin/go"
}

# ---- 探测 bun --------------------------------------------------------------
resolve_bun() {
  if [ -n "${BUN_BIN:-}" ]; then echo "$BUN_BIN"; return; fi
  if command -v bun >/dev/null 2>&1; then command -v bun; return; fi
  [ -x "$HOME/.bun/bin/bun" ] && { echo "$HOME/.bun/bin/bun"; return; }
  die "未找到 bun。请安装：curl -fsSL https://bun.sh/install | bash（或设置 BUN_BIN）"
}

VERSION="$(resolve_version)"
GO="$(resolve_go)"
log "版本号:  $VERSION"
log "Go:      $GO ($("$GO" version | awk '{print $3}'))"

# ---- 前端构建 --------------------------------------------------------------
if [ -n "${SKIP_FRONTEND:-}" ]; then
  warn "SKIP_FRONTEND 已设置，跳过前端构建（复用现有 dist）"
else
  BUN="$(resolve_bun)"
  log "Bun:     $BUN ($("$BUN" --version))"

  # 默认版与经典版对 date-fns 版本要求冲突（default 需 v4、classic 的 Semi 经
  # date-fns-tz 需 v2），共享 node_modules 会因版本提升相互污染。故与 CI/Docker
  # 一致：各自隔离安装（清空 node_modules 后再按 workspace 过滤安装）。
  clean_web_modules() { rm -rf web/node_modules web/default/node_modules web/classic/node_modules; }

  log "构建默认前端 (web/default)..."
  clean_web_modules
  ( cd web && "$BUN" install --frozen-lockfile )
  ( cd web/default && DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION="$VERSION" "$BUN" run build )

  log "构建经典前端 (web/classic)..."
  clean_web_modules
  ( cd web && "$BUN" install --filter ./classic --frozen-lockfile )
  ( cd web/classic && VITE_REACT_APP_VERSION="$VERSION" "$BUN" run build )
fi

[ -d web/default/dist ] || die "web/default/dist 不存在，无法 go:embed。请勿设置 SKIP_FRONTEND 或先构建前端。"
[ -d web/classic/dist ] || die "web/classic/dist 不存在，无法 go:embed。"

# ---- 后端交叉编译 ----------------------------------------------------------
mkdir -p "$OUTPUT_DIR"
LDFLAGS="-s -w -X '${MODULE_PATH}/common.Version=${VERSION}'"

for target in $BUILD_TARGETS; do
  os="${target%%/*}"; arch="${target##*/}"
  out="$OUTPUT_DIR/new-api-${VERSION}-${os}-${arch}"
  [ "$os" = "windows" ] && out="${out}.exe"
  log "编译后端 ${os}/${arch} -> $(basename "$out")"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    "$GO" build -trimpath -ldflags "$LDFLAGS" -o "$out" .
done

# ---- 校验和 ----------------------------------------------------------------
log "生成 SHA256 校验和..."
( cd "$OUTPUT_DIR" && \
  if command -v sha256sum >/dev/null 2>&1; then sha256sum new-api-* > checksums.txt; \
  else shasum -a 256 new-api-* > checksums.txt; fi )

log "完成 ✅ 产物位于: $OUTPUT_DIR"
ls -lh "$OUTPUT_DIR" | tail -n +2 | awk '{print "  " $9 "  " $5}'
