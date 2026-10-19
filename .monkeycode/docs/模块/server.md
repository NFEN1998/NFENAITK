# server

HTTP 服务层：注册所有路由，实现 OCS 查询接口、管理控制台 API、用户中心 API，并内嵌前端资源。

## 结构

```
internal/server/
├── server.go       # Server 结构、Deps、路由注册、中间件（CORS/日志）
├── query.go        # /query 查询链路与解析
├── admin.go        # 管理接口路由与设置/日志/统计/第三方题库
├── admin_users.go  # 用户管理接口
├── user_auth.go    # 用户令牌鉴权与用户中心路由
├── account_auth.go # 账号会话、CSRF、密码与来源校验
├── account_api.go  # 注册/登录/绑定/改密/卡密兑换
├── account_admin.go# 管理端账号与卡密接口
├── ratelimit.go    # 进程内注册/登录/兑换限流
├── model_api.go    # 模型配置与调用接口
├── setup.go        # 首次运行安装向导
├── console.go      # 控制台/用户中心页面渲染
├── pages.go        # 首页与模板数据
├── bus.go          # 事件总线（SSE 日志流）
├── singleflight.go # 同题并发合并
└── logcapture.go   # 日志持久化辅助
```

## 关键文件

| 文件 | 目的 |
| --- | --- |
| `server.go` | `Server` 与 `New`，路由注册 `Handler` |
| `query.go` | `handleQuery` 与 `resolveQueryUncached` 查询链路 |
| `admin.go` | `handleAdmin` 分派与设置、第三方题库接口 |
| `account_api.go` | 账号注册登录、token 绑定、卡密兑换 |
| `account_auth.go` | 会话 cookie、CSRF 与 bcrypt 密码校验 |
| `account_admin.go` | 管理端账号与卡密批次接口 |
| `setup.go` | 未配置数据库/令牌时的安装向导 |

## 依赖

**本模块依赖**
- `internal/store` - 题库、用户、日志与设置持久化
- `internal/externalbank` - 第三方题库查询
- `internal/ai` - 模型调用
- `internal/cache` - Redis 缓存与 SSE 日志队列
- `internal/match`, `internal/plan`, `internal/config`

## 规范

### 代码模式

**新增管理接口**：

```go
// handleAdmin 中增加分支
case "external-banks":
    s.adminExternalBanks(w, r, parts[1:])
```

### 错误处理
- 处理函数内用 `writeJSON` 返回 `{"success": bool, "message": string}`
- 运行期错误通过 `s.PublishError` 广播到控制台日志流

### 测试
- 集成测试在 `*_test.go`，通过 `testServer(t)` 连接 `TEST_DATABASE_URL`
- 外部依赖用 `httptest` 起本地服务
