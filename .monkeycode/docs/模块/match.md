# match

题目匹配算法：为查询与第三方答案校验提供题目归一化、稳定哈希与相似度评分。

## 结构

```
internal/match/
├── match.go       # 相似度评分、关键词覆盖、URL 归一
├── hash.go        # QuestionHash / OptionsHash / LookupKey、归一化
├── match_test.go
└── hash.go 对应测试
```

## 关键文件

| 文件 | 目的 |
| --- | --- |
| `hash.go` | `NormalizeQuestion`、`QuestionHash`（题面 + URL 集合）、`LookupKey`（精确查询与缓存键） |
| `match.go` | `Score`（模糊评分，供两段式查询与第三方校验使用）、`ExtractURLs` |

## 依赖

**本模块依赖**
- `github.com/yanyiwu/gojieba` - 中文分词（CGO）

**依赖本模块的**
- `internal/store` - 精确哈希查询与候选评分
- `internal/server` - 查询链路与第三方答案校验

## 规范

### 代码模式

**作为评分器传入存储层**：

```go
hits, err := s.store.Query(req.Title, req.Options, match.Score)
```

**第三方答案校验**（阈值 0.72）：

```go
if score, ok := match.Score(query, result.Question); !ok || score < 0.72 {
    // 丢弃
}
```

### 行为
- `Score` 返回 `(float64, bool)`：`ok=false` 表示不构成匹配
- `QuestionHash` 追加排序后的 URL 集合，不同图片题目不会共享哈希；`NormalizerVersion=3` 时启动会重算一次全库哈希
- 精确哈希命中跳过模糊评分；所有题目都要求 URL 集合一致（纯文本题为空集合）
- gojieba 词典随发布包附带，通过 `JIEBA_DICT_DIR` 或 exe 同级 `dict/` 加载
