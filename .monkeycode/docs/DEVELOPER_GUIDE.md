# nfentik-go 开发者指南

## 项目目的

nfentik-go 是 ZError 的 Go 服务端实现，用于为 OCS 网课助手提供本地 + 第三方 + AI 的多级题库查询服务。它在系统中担任后端服务角色，通过单一 HTTP 端口提供查询接口、管理控制台与用户中心。

**核心职责**
- 解析 `/query` 请求并返回答案（缓存 → 本地题库 → 第三方题库 → AI）
- 维护题库、文件夹、用户与套餐、模型与第三方题库配置
- 记录请求日志与用户使用记录，提供 Redis 缓存加速

**相关系统**
- PostgreSQL / MySQL - 持久化存储
- Redis - 可选缓存与计数
- AI 模型接口 / 第三方题库接口 - 两类外部答案来源

## 环境搭建

### 前置条件

- Go 1.25（`go.mod` 声明）或更高
- PostgreSQL 13+ 或 MySQL 5.7+ / MariaDB 10.2+
- CGO 工具链（`gcc`）：gojieba 与 go-sqlite3 依赖 CGO，`CGO_ENABLED=0` 构建会失败
- Redis 6+（可选）
- Windows 构建需 MinGW-w64（MSYS2）或交叉工具链

Debian / Ubuntu：

```bash
sudo apt-get update
sudo apt-get install -y build-essential postgresql redis-server
```

### 构建

```bash
cd nfentik-go
go build -o nfentik-go .
```

### 环境变量

| 变量 | 必需 | 描述 | 示例 |
| --- | --- | --- | --- |
| `DATABASE_URL` | 是 | 数据库连接串，优先级高于配置文件 | `postgres://user:pass@127.0.0.1:5432/nfentik?sslmode=disable` |
| `REDIS_URL` | 否 | Redis 连接串，设置后启用缓存 | `redis://127.0.0.1:6379/0` |
| `TEST_DATABASE_URL` | 否 | 测试用数据库 | `mysql://user:pass@127.0.0.1:3306/zerror` |
| `DB_MAX_OPEN_CONNS` | 否 | 连接池上限（默认 50） | `50` |
| `DB_MAX_IDLE_CONNS` | 否 | 连接池空闲上限（默认 25） | `25` |
| `AI_MAX_CONCURRENCY` | 否 | 模型并发上限（默认 64，0 为不限） | `64` |

数据库引擎由连接串前缀自动识别：`postgres://` 走 PostgreSQL，`mysql://` 走 MySQL/MariaDB。切勿提交任何密钥。

### 运行

```bash
export DATABASE_URL="mysql://nfentik:nfentik@127.0.0.1:3306/nfentik"
./nfentik-go -port 3000 -lan
```

参数：`-port`（HTTP 端口）、`-bind`（监听地址）、`-lan`（监听 `0.0.0.0`，默认仅 `127.0.0.1`）。首次启动会自动建表；未配置管理员令牌时进入安装向导 `/setup`。

运维子命令：

```bash
./nfentik-go check-db
./nfentik-go migrate-sqlite -src /path/to/database.db
```

## 开发工作流

### 代码质量工具

| 工具 | 命令 | 目的 |
| --- | --- | --- |
| 构建 | `go build ./...` | 编译检查 |
| 静态检查 | `go vet ./...` | 常见问题扫描 |
| 格式化 | `gofmt -l internal/ main.go` | 检查格式（`-w` 修复） |
| 测试 | `go test -p 1 ./...` | 单元与集成测试 |

### 测试约定

- 集成测试共用同一个数据库（`TEST_DATABASE_URL`），`internal/server` 与 `internal/store` 包会互相干扰，**必须串行**执行：`go test -p 1 ./...`。
- 无数据库时 `testStore` / `testServer` 会自动 `t.Skip`。
- 适配器类外部依赖用 `net/http/httptest` 起本地服务，避免真实外网。

```bash
TEST_DATABASE_URL="postgres://zerror:zerror@127.0.0.1:5432/zerror?sslmode=disable" go test -p 1 ./...
TEST_DATABASE_URL="mysql://zerror:zerror@127.0.0.1:3306/zerror" go test -p 1 ./...
```

### 打包发布

```bash
scripts/packaging/build.sh          # Linux amd64 tar.gz
scripts/packaging/build-windows.sh  # Windows amd64 zip（需 mingw-w64 交叉工具链）
```

产物在 `dist/`，包含二进制、`dict/`（gojieba 词典）、示例配置与说明。

## 常见任务

### 新增数据库迁移

**需修改的文件**
1. `internal/store/migrate.go` - 在 `migrations` 列表末尾追加 `migration`，`Version` 取当前最大值 +1
2. 若为 MySQL 也需同步 `mysqlColumnRepairs` / `mysqlTableDDL`（MySQL 无 `ADD COLUMN IF NOT EXISTS`）
3. 若属于核心列，同步 `coreColumns`

**步骤**
1. 优先使用幂等语句（PostgreSQL 可用 `ADD COLUMN IF NOT EXISTS`）
2. 重新编译后启动服务，自动应用并记录到 `SchemaMigrations`
3. 升级前先备份数据库

### 新增管理接口

**需修改的文件**
1. `internal/server/admin.go` - 在 `handleAdmin` 增加路由分支，实现处理函数
2. `internal/config` 或 `internal/store` - 需要持久化时增加读写方法
3. `web/static/console.js` + `web/templates/console.html` - 控制台视图（如需）

### 接入第三方题库

在控制台「第三方题库」视图新增源即可，无需改代码；字段含义见 `docs/INTERFACES.md`。若需扩展适配能力，改 `internal/externalbank`。

### 适配新的数据库引擎

在 `internal/store/dialect.go` 增加方言分支，补齐占位符重绑、upsert、自引用删除、递归遍历等差异，并在 `migrate.go` 提供对应建表语句。

## 编码规范

### 文件组织
- 每个包内按职责拆分文件（`store.go` / `migrate.go` / `dialect.go`）
- 测试文件与源码同目录，命名 `xxx_test.go`

### 命名

| 类型 | 约定 | 示例 |
| --- | --- | --- |
| 包 | 小写单词 | `externalbank` |
| 导出类型 | PascalCase | `Source` |
| 函数/方法 | camelCase | `Lookup` |
| 常量 | PascalCase 或 SCREAMING | `DefaultTimeoutSeconds` |

### 错误处理
- 返回 `error` 并携带上下文（`fmt.Errorf("...: %w", err)`）
- 可预期的“未命中”用哨兵错误区分（如 `externalbank.ErrNoAnswer`），调用方据此继续而非失败

### 日志
- 启动与迁移使用 `log.Printf`；运行期错误通过 `Server.PublishError` 广播到控制台
- 敏感字段（令牌、Authorization、apiKey）写入日志前必须脱敏

### SQL
- 统一使用 `$n` 占位符，由方言层重绑为 MySQL 的 `?`
- 时间格式化、upsert、NULL 安全比较等差异一律走 `dialect` 方法，不要内联方言语法
