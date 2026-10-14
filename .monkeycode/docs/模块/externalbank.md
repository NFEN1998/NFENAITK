# externalbank

第三方题库适配层：把管理员配置的任意 JSON 题库 HTTP 接口，转换为查询链路可用的统一查询。

## 结构

```
internal/externalbank/
├── externalbank.go       # Source / Settings / Query / Result 类型，Parse / Enabled / ValidURL
├── client.go             # Client.Lookup：请求构造、占位符渲染、响应解析、超时
├── externalbank_test.go  # 配置解析与过滤单测
└── client_test.go        # 适配器单测（httptest）
```

## 关键文件

| 文件 | 目的 |
| --- | --- |
| `externalbank.go` | 配置模型与 `Enabled()` 排序过滤 |
| `client.go` | 一次 Lookup 的完整实现；`ErrNoAnswer` 区分“未命中”与“失败” |

## 依赖

**本模块依赖**
- 标准库 `net/http`、`encoding/json`、`net/url`

**依赖本模块的**
- `internal/server` - 查询链路与管理接口

## 规范

### 代码模式

**执行一次查询**：

```go
client := externalbank.NewClient()
result, err := client.Lookup(ctx, src, externalbank.Query{Title: title, Options: opts, Type: typ})
if errors.Is(err, externalbank.ErrNoAnswer) {
    // 未命中：继续下一个源
}
```

### 错误处理
- 响应无答案返回 `ErrNoAnswer`（未命中）；网络/超时/非 2xx/非法 JSON 返回普通错误（失败）
- 两者在链路中都被视为“继续下一个源”，但失败会记录日志

### 测试
- 用 `httptest.NewServer` 模拟上游，覆盖 GET/POST、占位符、路径提取、空/非法响应与超时
