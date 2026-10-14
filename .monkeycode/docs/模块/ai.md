# ai

AI 模型调用层：在本地题库与第三方题库都未命中时，通过兼容 OpenAI 的接口调用配置的文本/视觉模型，并对返回内容做错误检测、答案提取与可信度校验。

## 结构

```
internal/ai/
├── ai.go         # Client、流式/非流式调用、并发限制
├── models.go     # Platform / Model / ModelSettings 及解析
├── prompt.go     # 提示词构建
├── validate.go   # 答案可信度校验（IsUnreliableAnswer）
├── regex.go      # 提取辅助
└── *_test.go     # 单测
```

## 关键文件

| 文件 | 目的 |
| --- | --- |
| `ai.go` | 模型调用核心与并发上限 |
| `models.go` | 模型与平台配置结构、`ParseModelSettings`、`TextModels` / `VisionModel` |
| `validate.go` | 拒绝空/低信息/拒答类不可信答案 |

## 依赖

**本模块依赖**
- 标准库 HTTP；调用外部 OpenAI 兼容接口

**依赖本模块的**
- `internal/server` - 查询链路的 AI 兜底阶段

## 规范

### 代码模式

**读取已选模型**：

```go
settings, _ := ai.ParseModelSettings(raw)
for _, m := range settings.TextModels() { /* 按优先级调用 */ }
```

### 错误处理
- 调用失败返回错误；返回内容先经 `DetectError` 判定是否为模型错误
- 答案经 `ExtractAnswer` 提取、`IsUnreliableAnswer` 校验后才回写题库

### 测试
- `validate_test.go`、`extract_test.go`、`concurrency_test.go` 覆盖校验、提取与并发
