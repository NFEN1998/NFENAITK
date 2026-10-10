# nfentik-go 安装包

本包为 nfentik-go 预编译发布版，适用于 Linux amd64，无需 Go 环境即可部署。

## 包内容

```
nfentik-go              # 预编译二进制（linux/amd64）
config/
  config.example.json   # 引导配置示例（数据库 / Redis 连接串）
install.sh              # 一键安装脚本（复制到 /opt/nfentik 并安装 systemd 服务）
nfentik-go.service      # systemd 服务单元
README.md               # 本文件
```

## 运行时依赖

| 组件 | 是否必需 | 最低版本 | 用途 |
| --- | --- | --- | --- |
| PostgreSQL | 必需 | 13+ | 题库、用户、日志、配置的持久化存储 |
| Redis | 可选 | 5+ | 查询缓存与日志队列；缺失时自动退化为直连数据库 |

安装数据库（Debian / Ubuntu）：

```bash
sudo apt-get update
sudo apt-get install -y postgresql redis-server
```

创建数据库用户与库：

```bash
sudo -u postgres psql -c "CREATE USER nfentik WITH PASSWORD 'nfentik';"
sudo -u postgres psql -c "CREATE DATABASE nfentik OWNER nfentik;"
```

## 安装

方式一：一键安装（推荐）

```bash
tar -xzf nfentik-go-*.tar.gz
cd nfentik-go
sudo ./install.sh
sudo systemctl enable --now nfentik-go
```

方式二：手动安装

```bash
sudo mkdir -p /opt/nfentik
sudo cp nfentik-go /opt/nfentik/
sudo cp config/config.example.json /opt/nfentik/config/config.json
sudo chmod 600 /opt/nfentik/config/config.json
```

编辑 `/opt/nfentik/config/config.json`，填入实际的数据库与 Redis 连接串。

## 首次启动：安装向导

首次运行（未配置数据库或管理员令牌为空）会进入安装向导模式：

```bash
cd /opt/nfentik
./nfentik-go -port 3000 -lan
```

浏览器访问 `http://<服务器地址>:3000`，按向导填写数据库连接、管理员令牌、站点名称。保存后**手动重启**程序生效。

## 参数说明

| 参数 | 说明 |
| --- | --- |
| `-port` | HTTP 端口，覆盖数据库中的设置 |
| `-bind` | 监听地址，覆盖数据库中的设置 |
| `-lan` | 监听 `0.0.0.0`，允许局域网访问；省略时仅监听 `127.0.0.1` |

## 运维命令

| 命令 | 用途 |
| --- | --- |
| `./nfentik-go` | 正常启动服务 |
| `./nfentik-go check-db` | 只读数据库完整性检测，结构不完整时退出非零 |
| `./nfentik-go migrate-sqlite -src <path>` | 从旧版 SQLite 数据库导入 |

连接串优先级：**环境变量 > 本地 `config/config.json`**。

## 验证安装

```bash
curl -s -o /dev/null -w "root=%{http_code}\n"    http://127.0.0.1:3000/
curl -s -o /dev/null -w "console=%{http_code}\n" http://127.0.0.1:3000/console
```

两者应返回 `200`。用管理员令牌登录控制台 `http://<地址>:3000/console`。

## 升级

升级前务必备份数据库：

```bash
pg_dump "postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable" > backup_$(date +%F).sql
```

替换二进制并重启，服务启动时会自动执行幂等迁移与结构修复，不会删除已有数据：

```bash
sudo cp nfentik-go /opt/nfentik/
sudo systemctl restart nfentik-go
```

## 卸载

```bash
sudo systemctl disable --now nfentik-go
sudo rm -f /etc/systemd/system/nfentik-go.service
sudo systemctl daemon-reload
```

数据库与 `/opt/nfentik` 目录请按需自行清理。
