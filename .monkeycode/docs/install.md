# nfentik-go 安装部署教程

本教程介绍如何在一台 Linux 服务器上从零安装并运行 nfentik-go。全文以 Debian / Ubuntu 为例，其他发行版请替换对应的包管理命令。

## 目录

1. [架构与前置条件](#1-架构与前置条件)
2. [安装依赖](#2-安装依赖)
3. [准备 PostgreSQL](#3-准备-postgresql)
4. [准备 Redis（可选）](#4-准备-redis可选)
5. [编译二进制](#5-编译二进制)
6. [首次启动：安装向导](#6-首次启动安装向导)
7. [验证安装](#7-验证安装)
8. [配置为 systemd 服务](#8-配置为-systemd-服务)
9. [升级](#9-升级)
10. [旧版 SQLite 数据迁移](#10-旧版-sqlite-数据迁移)
11. [运维命令](#11-运维命令)
12. [常见问题](#12-常见问题)

## 1. 架构与前置条件

nfentik-go 是单文件 Go 服务，前端管理控制台通过 `embed` 内嵌，无需 Node 构建。运行时依赖：

| 组件 | 是否必需 | 最低版本 | 用途 |
| --- | --- | --- | --- |
| Go | 编译时需要 | 1.24+ | 构建二进制（也可直接用预编译产物，无需 Go） |
| PostgreSQL | 必需 | 13+ | 题库、用户、日志、配置的唯一持久化存储 |
| Redis | 可选 | 5+ | 查询缓存、每日计数、请求日志缓冲；缺失时自动退化为直连数据库 |
| CGO / gcc | 编译时需要 | - | 中文分词 gojieba 依赖 CGO |

重要概念：**所有业务配置（管理员令牌、模型配置、站点名、用户与套餐等）都保存在 PostgreSQL 的 `AppSettings` / `Users` 等表中**。只要始终连接同一个数据库，程序放在哪个目录、重装多少次都不会丢配置。本地文件只保留连接串。

本地文件布局（相对于二进制所在目录）：

```
/opt/nfentik/
  nfentik-go          # 可执行文件
  config/
    config.json       # 引导配置：仅数据库与 Redis 连接串（权限 600）
```

## 2. 安装依赖

```bash
sudo apt-get update
sudo apt-get install -y build-essential postgresql redis-server
```

`build-essential` 提供 `gcc`，满足 CGO 编译要求。仅运行预编译二进制时无需安装它。

## 3. 准备 PostgreSQL

创建数据库用户与库：

```bash
sudo -u postgres psql -c "CREATE USER nfentik WITH PASSWORD 'nfentik';"
sudo -u postgres psql -c "CREATE DATABASE nfentik OWNER nfentik;"
```

按需赋予扩展权限（用于模糊匹配的 `pg_trgm` 是可选扩展，缺失时服务会优雅降级，不影响启动）：

```bash
sudo -u postgres psql -d nfentik -c "CREATE EXTENSION IF NOT EXISTS pg_trgm;"
```

连接串格式：

```
postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable
```

生产环境建议启用 SSL（`sslmode=require`）并使用强密码。

## 4. 准备 Redis（可选）

```bash
sudo systemctl enable --now redis-server
```

连接串格式：`redis://127.0.0.1:6379/0`。

Redis 不可用时服务会自动退化为直接读写 PostgreSQL，功能不受影响，只是失去缓存加速与写入缓冲。

## 5. 编译二进制

```bash
git clone https://github.com/NFEN1998/NFENAITK.git
cd NFENAITK/nfentik-go

gofmt -l internal/
go build -o nfentik-go .
```

注意：**不要设置 `CGO_ENABLED=0`**，中文分词与旧库迁移依赖 CGO，禁用后会编译失败。

部署到运行目录：

```bash
sudo mkdir -p /opt/nfentik
sudo cp nfentik-go /opt/nfentik/
```

## 6. 首次启动：安装向导

首次运行时若「未配置数据库连接」或「数据库可连但管理员令牌为空」，程序不会进入正常服务，而是以**安装向导模式**启动，把所有请求重定向到 `/setup`，避免实例以未受管状态暴露。

启动（首次可只用环境变量提供连接串）：

```bash
export DATABASE_URL="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable"
export REDIS_URL="redis://127.0.0.1:6379/0"
/opt/nfentik/nfentik-go -port 3000 -lan
```

### 参数说明

| 参数 | 说明 |
| --- | --- |
| `-port` | HTTP 端口，覆盖数据库中的设置 |
| `-bind` | 监听地址，覆盖数据库中的设置 |
| `-lan` | 允许局域网访问；默认仅监听 `127.0.0.1` |

### 向导步骤

1. 浏览器访问 `http://<服务器地址>:3000`，自动跳转到 `/setup`。
2. 填写数据库连接字段：主机、端口、用户名、密码、库名、SSL 模式。
3. 点击「测试连接」，确认数据库可达（对应 `POST /setup/test-db`）。
4. 按需填写 Redis 连接。
5. 设置**管理员令牌**（必填）、站点名称与副标题。
6. 提交保存（`POST /setup`）。保存时会再次校验数据库可达，然后：
   - 写入本地 `config/config.json`（引导配置）；
   - 若数据库可连，同时把设置持久化到数据库。

保存成功后**需要手动重启程序**使配置生效：

```bash
# 先停止当前进程（前台运行时用 Ctrl+C）
# 再重新启动
/opt/nfentik/nfentik-go -port 3000 -lan
```

重启后，若检测到管理员令牌已配置，`/setup` 将返回 404，服务进入正常模式。

## 7. 验证安装

安装向导完成后重启，检查以下内容。

查看启动日志（应包含完整性检测、连接串脱敏等信息）：

```
数据库完整性检测: 通过（版本 5/5，结构与迁移均为最新）
已连接 PostgreSQL: postgres://nfentik:***@127.0.0.1:5432/nfentik?sslmode=disable
配置已从数据库加载
已连接 Redis，查询缓存与日志队列已启用
nfentik-go 服务已启动: http://0.0.0.0:3000
管理控制台:   http://0.0.0.0:3000/console
```

健康检查：

```bash
curl -s -o /dev/null -w "root=%{http_code}\n"    http://127.0.0.1:3000/
curl -s -o /dev/null -w "console=%{http_code}\n" http://127.0.0.1:3000/console
```

两者应返回 `200`。用管理员令牌登录控制台 `http://<地址>:3000/console`，即可继续配置模型、站点与用户套餐。

查询接口自测：

```bash
curl -X POST http://127.0.0.1:3000/query \
  -H 'Content-Type: application/json' \
  -d '{"title":"题目内容","options":"A. ... B. ...","type":"single_choice"}'
```

## 8. 配置为 systemd 服务

向导参数验证无误后，建议交给 systemd 常驻运行。

新建 `/etc/systemd/system/nfentik-go.service`：

```ini
[Unit]
Description=nfentik-go question bank service
After=network.target postgresql.service redis-server.service

[Service]
Type=simple
User=nfentik
WorkingDirectory=/opt/nfentik
Environment=DATABASE_URL=postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable
Environment=REDIS_URL=redis://127.0.0.1:6379/0
ExecStart=/opt/nfentik/nfentik-go -port 3000 -lan
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

启用并启动：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now nfentik-go
sudo systemctl status nfentik-go
```

说明：使用 systemd 后，连接串直接由 `Environment=` 提供即可，本地 `config.json` 非必需（环境变量优先级更高）。

## 9. 升级

**升级前务必先备份数据库**，迁移为前向单向，不提供自动回滚：

```bash
pg_dump "postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable" > backup_$(date +%F).sql
```

拉取新代码重新编译，然后用只读命令预检数据库：

```bash
cd NFENAITK/nfentik-go
go build -o nfentik-go .
DATABASE_URL="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable" \
  ./nfentik-go check-db
```

`check-db` 只读检查，不修改任何数据。结构完整时退出码为 `0`，输出如下；若结构不完整则退出码非零，可用于部署门禁：

```
数据库完整性检测报告
  数据库:       postgres://nfentik:***@127.0.0.1:5432/nfentik?sslmode=disable
  版本表:       存在
  当前版本:     5
  目标版本:     5
  缺失表:       无
  缺失字段:     无
  待应用迁移:   无
  状态:         通过（结构与迁移均为最新）
```

替换二进制并重启：

```bash
sudo cp nfentik-go /opt/nfentik/
sudo systemctl restart nfentik-go
```

启动时服务会自动：执行待应用的迁移；对缺失的表/字段做幂等修复（`CREATE TABLE IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`）；回填题目哈希并建索引。**升级不会删除或重建已有数据**。结构修复只恢复表与字段，不恢复已删除的数据，数据安全依赖备份。

## 10. 旧版 SQLite 数据迁移

若从旧版桌面端迁移，可导入旧 `database.db`：

```bash
export DATABASE_URL="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable"
./nfentik-go migrate-sqlite -src /path/to/old/database.db
```

迁移保留文件夹层级、题目内容、AI 标记与待修正标记，空答案题目会跳过，输出中给出统计。

## 11. 运维命令

| 命令 | 用途 |
| --- | --- |
| `./nfentik-go` | 正常启动服务（进入安装向导或正常模式） |
| `./nfentik-go check-db` | 只读数据库完整性检测，输出报告，结构不完整时退出非零 |
| `./nfentik-go migrate-sqlite -src <path>` | 从旧 SQLite 数据库导入 |

数据库与 Redis 连接串的优先级：**环境变量 > 本地 `config/config.json`**。

## 12. 常见问题

**Q：启动后访问任意页面都跳到 `/setup`？**
两个原因之一：尚未配置数据库连接，或数据库可连但管理员令牌为空。完成向导并重启即可。

**Q：保存向导后仍进入向导？**
向导保存后需要手动重启程序使配置生效。systemd 部署时执行 `sudo systemctl restart nfentik-go`。

**Q：编译报 CGO 相关错误？**
确认已安装 `gcc`（`build-essential`），且未设置 `CGO_ENABLED=0`。

**Q：Redis 连接失败？**
Redis 是可选的。连接失败时日志会打印「Redis 不可用，已退化为仅使用数据库」，服务继续正常工作。

**Q：换服务器 / 重装二进制后配置丢失？**
配置存在数据库里。确保连接同一个数据库即可；本地只需保证 `config/config.json` 或环境变量里的连接串正确。

**Q：数据库结构出现漂移（手工改过表、部分还原）？**
服务启动时会自动检测并幂等修复缺失的表与字段，不影响已有数据。可先用 `check-db` 查看检测结果。
