#!/bin/bash
set -euo pipefail

# 构建 nfentik-go 的 Windows amd64 发布包（zip）。
#
# 用法：scripts/packaging/build-windows.sh [输出目录]
#
# 支持两种编译方式：
#   1. 交叉编译（在 Linux 上构建 Windows 产物）
#      需先安装 mingw-w64 交叉工具链：
#        sudo apt-get install -y gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64
#   2. 原生编译（在 Windows 的 MSYS2 / MinGW 环境里直接运行本脚本）
#
# 依赖：go 工具链、zip。
# 注意：gojieba 与 go-sqlite3 均依赖 CGO，无法用纯 Go 方式交叉编译。

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR/dist}"
ASSETS_DIR="$ROOT_DIR/scripts/packaging/assets"

GOARCH_VAL="${GOARCH:-amd64}"
VERSION="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo dev)"
NAME="nfentik-go-${VERSION}-windows-${GOARCH_VAL}"
STAGE="$(mktemp -d)"
PKG_ROOT="$STAGE/nfentik-go"

cleanup() { rm -rf "$STAGE"; }
trap cleanup EXIT

echo "版本: $VERSION"
echo "目标: windows/${GOARCH_VAL}"
echo "输出: $OUT_DIR/$NAME.zip"

case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)
    echo "检测到 Windows 宿主环境，使用本机 MinGW 工具链编译"
    ;;
  *)
    if ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
      echo "未找到 mingw-w64 交叉编译器，请先安装：" >&2
      echo "  sudo apt-get install -y gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64" >&2
      exit 1
    fi
    export CC=x86_64-w64-mingw32-gcc
    export CXX=x86_64-w64-mingw32-g++
    ;;
esac

# 静态链接 C/C++ 运行库，避免目标机器缺少 libstdc++-6.dll / libgcc / libwinpthread。
export CGO_ENABLED=1
export CGO_LDFLAGS="-static-libgcc -static-libstdc++ -static"

mkdir -p "$PKG_ROOT/config" "$OUT_DIR"

# 与 Linux 构建保持一致：不要使用 -trimpath，否则 gojieba 无法定位内嵌词典。
GOOS=windows GOARCH="$GOARCH_VAL" \
  go build -C "$ROOT_DIR" -ldflags "-s -w" -o "$PKG_ROOT/nfentik-go.exe" .

cp "$ASSETS_DIR/config.example.json" "$PKG_ROOT/config/config.example.json"
cp "$ASSETS_DIR/README-windows.md" "$PKG_ROOT/README.md"
cp "$ASSETS_DIR/start.bat" "$PKG_ROOT/start.bat"

# 打包 gojieba 词典：gojieba 默认按编译机上的源码路径找词典，交叉编译出的
# 二进制在别的机器上会因缺词而启动失败，因此随包附带 dict/，由程序在 exe
# 同级目录自动加载。
GOJIEBA_DIR="$(cd "$ROOT_DIR" && go list -m -f '{{.Dir}}' github.com/yanyiwu/gojieba)"
cp -r "$GOJIEBA_DIR/deps/cppjieba/dict" "$PKG_ROOT/dict"

( cd "$STAGE" && zip -r "$OUT_DIR/$NAME.zip" nfentik-go >/dev/null )

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$OUT_DIR/$NAME.zip" > "$OUT_DIR/$NAME.zip.sha256"
fi

echo "完成:"
ls -la "$OUT_DIR/$NAME.zip"
