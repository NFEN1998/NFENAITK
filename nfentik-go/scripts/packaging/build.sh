#!/bin/bash
set -euo pipefail

# 构建 nfentik-go 的 Linux amd64 发布包（tar.gz）。
# 用法：scripts/packaging/build.sh [输出目录]
# 依赖：go 工具链、tar、gzip。

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR/dist}"
ASSETS_DIR="$ROOT_DIR/scripts/packaging/assets"

GOOS_VAL="${GOOS:-linux}"
GOARCH_VAL="${GOARCH:-amd64}"

VERSION="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo dev)"
NAME="nfentik-go-${VERSION}-${GOOS_VAL}-${GOARCH_VAL}"
STAGE="$(mktemp -d)"
PKG_ROOT="$STAGE/nfentik-go"

cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT

echo "版本: $VERSION"
echo "目标: ${GOOS_VAL}/${GOARCH_VAL}"
echo "输出: $OUT_DIR/$NAME.tar.gz"

mkdir -p "$PKG_ROOT/config" "$OUT_DIR"

# 注意：不要使用 -trimpath，否则 gojieba 无法定位内嵌词典。
GOOS="$GOOS_VAL" GOARCH="$GOARCH_VAL" CGO_ENABLED=1 \
  go build -C "$ROOT_DIR" -ldflags "-s -w" -o "$PKG_ROOT/nfentik-go" .

install -m 0644 "$ASSETS_DIR/config.example.json" "$PKG_ROOT/config/config.example.json"
install -m 0644 "$ASSETS_DIR/nfentik-go.service" "$PKG_ROOT/nfentik-go.service"
install -m 0755 "$ROOT_DIR/scripts/packaging/install.sh" "$PKG_ROOT/install.sh"
install -m 0644 "$ASSETS_DIR/README.md" "$PKG_ROOT/README.md"

tar -czf "$OUT_DIR/$NAME.tar.gz" -C "$STAGE" nfentik-go
sha256sum "$OUT_DIR/$NAME.tar.gz" > "$OUT_DIR/$NAME.tar.gz.sha256"

echo "完成:"
ls -la "$OUT_DIR/$NAME.tar.gz" "$OUT_DIR/$NAME.tar.gz.sha256"
