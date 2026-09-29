# nfentik-go

nfentik-go：由 ZError 的 Go 语言重写而来，把原本的 Tauri 桌面 AI 题库软件改造成可在 Linux 上部署的网页服务。前端管理控制台使用 Go `embed` 内嵌，最终产物是一个单文件二进制，无需 Node 构建。

数据层使用 PostgreSQL，并用 Redis 做查询缓存、每日计数与请求日志缓冲。

## 功能

- 内置 OCS 题库接口，用户脚本把题目发到 `/query` 即可查询答案。
- 题库命中直接返回本地答案；未命中时自动调用配置的 AI 模型，并把答案回写题库。
- 支持多文本模型并行调用与视觉模型（图片题）。
- 管理控制台提供题库、文件夹、待修正、模型配置、系统设置、实时请求日志六个视图。
- 提供旧版 SQLite 数据库到 PostgreSQL 的迁移命令。

## 架构

```
OCS 用户脚本 ──> /query
                    │
                    ▼
             查询缓存 (Redis)  ──命中──> 直接返回
                    │未命中
                    ▼
             题库匹配 (PostgreSQL) ──命中──> 返回本地答案
                    │未命中
                    ▼
             AI 模型调用 ──> 提取答案 ──> 写回题库 + 写入缓存
```

Redis 的三个用途：

1. **查询缓存**：以题目文本为键缓存 AI 答案，题库发生任何改动时自动整体失效，避免脏数据。
2. **每日请求计数**：`/query` 计数先在 Redis 累加，后台每 20 秒批量落库到 `DailyRequestCounts`。
3. **请求日志缓冲**：请求日志写入 Redis 列表，后台每 3 秒批量写入 `RequestLogs`，失败会重新入队。

Redis 不可用时服务自动退化为直接读写 PostgreSQL，功能不受影响。

## 环境要求

- Go 1.24 或更高版本
- PostgreSQL 13 或更高版本
- Redis 6 或更高版本（可选，用于缓存加速）
- CGO 环境（`gcc`）。中文分词依赖 gojieba，且迁移旧数据依赖 go-sqlite3，因此需要 `gcc` 与 SQLite 开发头文件。

Debian / Ubuntu 安装依赖：

```bash
sudo apt-get update
sudo apt-get install -y build-essential postgresql redis-server
```

## 配置

配置分为两部分：

**1. 本地引导配置**（仅连接串）

程序目录下的 `config/config.json`，只保存数据库与 Redis 连接信息，用于启动时连上数据库：

```json
{
  "database": { "url": "postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable" },
  "redis": {
    "url": "redis://127.0.0.1:6379/0",
    "enabled": true,
    "prefix": "nfentik",
    "queryCacheTTL": 600
  }
}
```

也可用环境变量提供，环境变量优先级更高：

```bash
export DATABASE_URL="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable"
export REDIS_URL="redis://127.0.0.1:6379/0"
```

`queryCacheTTL` 单位为秒，默认 600。

**2. 数据库配置**（系统设置 + 模型配置）

主题、语言、超时、管理令牌、多用户、AI 平台与模型等全部保存在数据库的 `AppSettings` 表中。因此**换机器、换目录、重装二进制都不会丢配置**，只要能连到同一个数据库即可。这些内容可在管理控制台里可视化修改。

> 升级提示：如果数据库里还没有配置，而程序目录下存在旧的 `config.json` / `model_config.json`，服务会在首次启动时自动导入数据库并删除这两个旧文件，随后写回一份只含连接串的引导配置。

## 数据库准备

```bash
sudo -u postgres psql -c "CREATE USER nfentik WITH PASSWORD 'nfentik';"
sudo -u postgres psql -c "CREATE DATABASE nfentik OWNER nfentik;"
```

服务首次启动会自动建表。

## 构建

```bash
cd /workspace/nfentik-go
go build -o nfentik-go .
```

## 运行

二进制可以放在任意目录，配置从数据库读取：

```bash
export DATABASE_URL="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable"
export REDIS_URL="redis://127.0.0.1:6379/0"
/opt/nfentik/nfentik-go -port 3000 -lan
```

常用参数：

| 参数 | 说明 |
| --- | --- |
| `-port` | HTTP 端口，覆盖数据库中的设置 |
| `-bind` | 监听地址，覆盖数据库中的设置 |
| `-lan` | 允许局域网访问，默认仅监听 `127.0.0.1` |

启动后控制台地址为 `http://127.0.0.1:3000/console`。

## 数据持久化

题目、文件夹、请求日志、系统设置与模型配置**全部保存在 PostgreSQL** 中。服务启动时会先执行数据库迁移，**不会**删除或重建任何已有数据。

只要**始终连接同一个数据库**，程序放在哪个目录、重启多少次都不会丢数据。启动日志会打印引导配置路径与脱敏后的连接串，可据此确认连到了预期位置：

```
引导配置: /opt/nfentik/config/config.json
已连接 PostgreSQL: postgres://nfentik:***@127.0.0.1:5432/nfentik?sslmode=disable
配置已从数据库加载
```

## 数据库迁移

服务内置带版本号的迁移系统，所有表结构变化都通过 `internal/store/migrate.go` 中 `migrations` 列表统一管理，无需手工执行 SQL。

- **版本表**：`SchemaMigrations` 记录每个已应用的迁移（`Version`、`Name`、`AppliedAt`）。
- **顺序执行**：`Version` 严格递增，启动时只执行尚未应用的迁移，已应用的重启不会重复执行。
- **事务保障**：单个迁移的所有语句与版本记录在同一事务内提交；任一步失败即整体回滚，进程以非零码退出并打印失败原因，不会留下半成品结构。
- **旧库兼容**：若数据库在引入迁移系统之前就已存在（有业务表但没有 `SchemaMigrations`），启动时会自动把基线版本标记为已应用，**不会重建表、不会动数据**，随后继续执行基线之后的新迁移。
- **查看版本**：控制台概览的「数据库结构版本」与 `GET /api/admin/stats` 的 `schema_version` 字段会显示当前最高版本。

新增一次结构变更的步骤：

1. 在 `migrations` 列表末尾追加一个 `migration`，`Version` 取当前最大值 +1，`Name` 用简短的英文描述。
2. 若只是给已有表加列，优先写成幂等语句（如 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`）。
3. 重新编译部署，服务下次启动会自动应用；如需回退，请先备份数据库再处理。

**升级前请先备份数据库**（`pg_dump`），迁移为前向单向，不提供自动回滚。


## 迁移旧 SQLite 数据

把旧版的 `database.db` 导入 PostgreSQL：

```bash
export DATABASE_URL="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable"
./nfentik-go migrate-sqlite -src /path/to/old/database.db
```

迁移会保留文件夹层级、题目内容、AI 标记与待修正标记。空答案题目会被跳过，输出中会给出统计。

## OCS 集成

在 OCS 用户脚本中把查询地址指向服务：

```
http://<服务器地址>:<端口>/query
```

接口支持 GET 与 POST：

```bash
# POST 查询
curl -X POST http://127.0.0.1:3000/query \
  -H 'Content-Type: application/json' \
  -d '{"title":"题目内容","options":"A. ... B. ...","type":"single_choice"}'
```

```bash
# GET 查询
curl "http://127.0.0.1:3000/query?title=题目内容"
```

返回格式：

```json
{
  "code": 1,
  "data": {
    "id": 12,
    "question": "题目内容（含标记为待修正按钮）",
    "answer": "答案",
    "is_ai": true,
    "is_pending_correction": false
  }
}
```

`code` 为 `1` 表示成功，`0` 表示失败，失败时 `message` 字段包含原因。

## 主要接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/POST | `/query` | OCS 题库查询 |
| GET | `/api/status` | 服务状态 |
| POST | `/api/questions/{id}/pending-correction` | 标记题目待修正 |
| GET | `/api/models` | 已配置的模型列表 |
| POST | `/api/model/call` | 单模型调用，返回完整结果 |
| POST | `/api/model/stream` | 单模型调用，SSE 逐 token 返回 |
| GET | `/api/logs/stream` | 请求日志 SSE 流 |
| POST | `/api/login` | 管理令牌校验 |
| GET | `/console` | 管理控制台页面 |

管理接口位于 `/api/admin/`，需要在请求头携带令牌：

```
Authorization: Bearer <adminToken>
```

当数据库中的 `adminToken` 为空时，管理接口不校验令牌。

## 模型配置

控制台的「模型配置」视图可视化编辑模型，保存在数据库的 `AppSettings` 表中，格式示例：

```json
{
  "selectedTextModels": ["gpt-4o-mini"],
  "selectedVisionModel": null,
  "platforms": [
    {
      "id": "openai",
      "name": "openai",
      "displayName": "OpenAI",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "sk-...",
      "enabled": true,
      "models": [
        {
          "id": "gpt-4o-mini",
          "name": "gpt-4o-mini",
          "displayName": "GPT-4o mini",
          "platformId": "openai",
          "maxTokens": 4096,
          "temperature": 0.3,
          "topP": 1,
          "enabled": true,
          "category": "text"
        }
      ]
    }
  ]
}
```

只要平台兼容 OpenAI 的 `/v1/chat/completions` 流式接口即可接入。

## systemd 部署

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

启用服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now nfentik-go
sudo systemctl status nfentik-go
```

## 开发

```bash
go build ./...
go vet ./...
DATABASE_URL="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable" \
REDIS_URL="redis://127.0.0.1:6379/0" \
go run . -port 3000
```

源码结构：

```
internal/config   配置读写
internal/store    PostgreSQL 存储与题库查询
internal/cache    Redis 查询缓存、计数与日志队列
internal/match    题目匹配算法（jieba 分词）
internal/ai       AI 模型调用与结果解析
internal/importer 旧 SQLite 数据迁移
internal/server   HTTP 服务、路由与控制台
web/templates     控制台模板（内嵌）
web/static        控制台静态资源（内嵌）
```
