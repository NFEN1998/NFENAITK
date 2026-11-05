# 需求实施计划

- [x] 1. 选择题答案校验
  - [x] 1.1 新增 `internal/ai/choice.go`：选项解析、单选/多选/判断校验、翻译与散文拒绝
  - [x] 1.2 在 AI 基础答案、最终答案与模型测试接口中接入 `ValidateAnswer`
  - [x] 1.3 提示词明确要求选择题返回选项文字并禁止翻译
  - [x] 1.4 管理端新增/编辑题目时校验答案，阻止人工误存翻译

- [x] 2. 图片题目身份隔离
  - [x] 2.1 `QuestionHash` 追加排序后的 URL 集合；`OptionsHash` 保持历史算法
  - [x] 2.2 `NormalizerVersion` 升级到 3，启动时重算全库 `QuestionHash`
  - [x] 2.3 精确查询、模糊候选与 `FindByNormalizedQuestion` 要求 URL 集合一致

- [x] 3. 第三方回写精确性
  - [x] 3.1 `acceptExternal` 增加选项校验与 URL 集合比较，返回 `exact`
  - [x] 3.2 近似匹配只返回当前请求，不回写、不缓存
  - [x] 3.3 仅精确匹配允许回写；回写后重新读取题库行

- [x] 4. 并发写回一致性
  - [x] 4.1 AI 回写后读取题库行构造响应与缓存
  - [x] 4.2 第三方回写后读取题库行构造响应与缓存
  - [x] 4.3 回写失败或 URL 身份不一致时不写缓存

- [x] 5. 测试与验证
  - [x] 5.1 `internal/ai` 选项校验与判断题测试
  - [x] 5.2 `internal/match` URL 哈希隔离测试
  - [x] 5.3 `internal/server` 第三方精确性与模型响应保存测试
  - [x] 5.4 `gofmt`、`go build ./...`、`go vet ./...` 通过
  - [x] 5.5 `go test -count=1 -p 1 ./...` 除既有 `TestQueryGetAcceptsQAlias` 外全部通过
