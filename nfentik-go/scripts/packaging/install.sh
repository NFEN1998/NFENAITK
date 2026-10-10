#!/bin/bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/nfentik}"
SERVICE_NAME="nfentik-go"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$(id -u)" -ne 0 ]; then
  echo "请以 root 运行：sudo ./install.sh" >&2
  exit 1
fi

if [ ! -f "$SCRIPT_DIR/nfentik-go" ]; then
  echo "未找到二进制文件 nfentik-go，请确认在解压目录内运行。" >&2
  exit 1
fi

echo "安装目录: $INSTALL_DIR"
mkdir -p "$INSTALL_DIR/config"

echo "复制二进制..."
install -m 0755 "$SCRIPT_DIR/nfentik-go" "$INSTALL_DIR/nfentik-go"

if [ ! -f "$INSTALL_DIR/config/config.json" ]; then
  if [ -f "$SCRIPT_DIR/config/config.example.json" ]; then
    install -m 0600 "$SCRIPT_DIR/config/config.example.json" "$INSTALL_DIR/config/config.json"
    echo "已生成 $INSTALL_DIR/config/config.json（请按需修改连接串）"
  fi
else
  echo "保留已有配置文件 $INSTALL_DIR/config/config.json"
fi

if [ -f "$SCRIPT_DIR/nfentik-go.service" ]; then
  echo "安装 systemd 服务..."
  install -m 0644 "$SCRIPT_DIR/nfentik-go.service" "/etc/systemd/system/${SERVICE_NAME}.service"
  systemctl daemon-reload
  echo "服务已安装。启动：systemctl enable --now ${SERVICE_NAME}"
fi

echo "完成。首次启动后浏览器访问 http://<服务器地址>:3000 进入安装向导。"
