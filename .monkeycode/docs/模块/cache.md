# cache

Redis 缓存层（可选）：提供查询结果缓存、每日请求计数与请求日志缓冲；Redis 不可用时服务自动退化为直接读写数据库。

## 结构

```
internal/cache/
├── cache.go  # Client：连接、查询缓存、每日计数、日志队列
└── hash.go   # 缓存键哈希辅助
```

## 关键文件

| 文件 | 目的 |
| --- | --- |
| `cache.go` | `GetQuery` / `SetQuery` / `InvalidateQueries`、`BumpDailyRequest`、`PushLog` / `QueueLength` |

## 依赖

**本模块依赖**
- `github.com/redis/go-redis/v9`
- 通过注入的 `LogSink` / `CountSink` 回调落库（不直接依赖存储层）

**依赖本模块的**
- `internal/server` - 查询缓存与实时日志

## 规范

### 代码模式

**查询缓存**：

```go
if s.cache != nil && s.cache.GetQuery(title, options, &entry) { /* 命中 */ }
s.cache.SetQuery(title, options, entry, ttl)
```

### 错误处理
- 构造函数连接失败时返回错误，`main` 会记录警告并令 `cache` 为 nil，服务继续以数据库直连运行
- 缓存失效策略：题库发生任何改动时整体失效（版本号 + 前缀）

### 行为
- 每日计数在 Redis 累加，后台批量落库
- 请求日志写入 Redis 列表，后台批量落库；失败重新入队
