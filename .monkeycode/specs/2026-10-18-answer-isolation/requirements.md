# Requirements Document

## Introduction

修复 OCS、第三方题库与 AI 并发写回时答案串题、错题入库与缓存/题库不一致的问题。问题表现为：选择题被 AI 返回题目翻译或散文，翻译被当作答案写入题库；不同图片题共享归一化哈希；第三方近似匹配使用原查询题面回写；并发写入时缓存保存了未落库的答案。

本需求把答案隔离与选项校验放在回写之前，确保题库只收录与题目、选项和图片身份一致的答案。

## Glossary

- **Choice Validation（选项校验）**：把单选/多选答案解析为选项字母或选项文字，拒绝翻译、复述题干和无关散文。
- **URL Identity（URL 身份）**：题目中排序后的 URL 集合；相同题面但不同图片属于不同题目。
- **Exact External Match（第三方精确匹配）**：第三方返回的题干与查询题干完全一致，且 URL 集合相同。
- **Bank-Cache Consistency（库缓存一致）**：并发写入后，返回与缓存使用题库实际保存的答案。
- **Normalizer Version（归一化版本）**：哈希算法版本；算法变化时启动后重算一次全库 `QuestionHash`。

## Requirements

### Requirement 1：选择题答案校验

**User Story:** AS 运营者, I want AI 与第三方答案必须匹配选项, so that 翻译和散文不会进入题库。

#### Acceptance Criteria

1. WHEN 题型为单选题且提供选项, THE System SHALL 接受选项字母或与某个选项一致的答案文字。
2. WHEN 题型为多选题且提供选项, THE System SHALL 支持 `###`、逗号、换行与连写字母，并校验每个答案都能解析到选项。
3. IF 单选题答案包含多个答案, THE System SHALL 拒绝该答案。
4. IF 选择题答案无法解析到任何选项, THE System SHALL 拒绝该答案且不写入题库与缓存。
5. WHEN 题型为判断题, THE System SHALL 把常见同义表达归一为「正确」或「错误」。
6. WHEN 题型为空且提供选项, THE System SHALL 按选择题校验。
7. WHEN 题型为填空或简答, THE System SHALL 保留答案原文并继续过滤拒答类内容。
8. WHEN 管理员新增或编辑题目并提交非空答案, THE System SHALL 执行同样的选择题校验，拒绝不匹配的答案。

### Requirement 2：图片题目身份隔离

**User Story:** AS 用户, I want 不同图片题目互不串答案, so that 图片 A 的答案不会被图片 B 使用。

#### Acceptance Criteria

1. THE System SHALL 在 `QuestionHash` 中包含排序后的 URL 集合。
2. WHEN 两道题题面相同但 URL 集合不同, THE System SHALL 生成不同的 `QuestionHash`。
3. THE System SHALL 在单飞键、缓存键、精确查询与唯一索引中使用新的 `QuestionHash`。
4. WHEN 本地题库精确查询或模糊候选的 URL 集合不一致, THE System SHALL 跳过该候选。
5. WHEN 归一化版本从 2 升级到 3, THE System SHALL 在启动时重算一次全库 `QuestionHash`。
6. THE System SHALL 保持 `OptionsHash` 历史算法不变，避免额外重算选项哈希。

### Requirement 3：第三方回写精确性

**User Story:** AS 运营者, I want 第三方答案只在题干完全一致时回写, so that 近似题不会污染题库。

#### Acceptance Criteria

1. THE System SHALL 对第三方答案执行题型与选项校验。
2. WHEN 第三方返回的题干与查询题干完全一致且 URL 集合相同, THE System SHALL 允许按来源配置回写与缓存。
3. WHEN 第三方返回的题干仅为近似匹配, THE System SHALL 把答案返回给当前请求且不写入题库与缓存。
4. WHEN 第三方未返回题干且来源未声明可信, THE System SHALL 拒绝该结果。
5. WHEN 第三方未返回题干且来源声明可信, THE System SHALL 返回答案给当前请求且不回写与缓存。
6. WHEN 第三方返回的题干 URL 集合不同, THE System SHALL 拒绝该结果。

### Requirement 4：并发写回一致性

**User Story:** AS 用户, I want 并发查询看到一致的答案, so that 缓存与题库不会返回不同结果。

#### Acceptance Criteria

1. WHEN AI 答案回写完成, THE System SHALL 重新读取题库行，并用题库答案构造响应与缓存。
2. WHEN 第三方答案回写完成, THE System SHALL 重新读取题库行，并用题库答案构造响应与缓存。
3. IF 回写未成功或返回的行 URL 身份不一致, THE System SHALL 不写入缓存。
4. WHEN 同一题目并发写入, THE System SHALL 保证题库只保留一行，所有调用方使用该行答案。

### Requirement 5：兼容与可观测

**User Story:** AS 维护者, I want 升级行为可预期, so that 现有 token、题库和查询链路不受影响。

#### Acceptance Criteria

1. THE System SHALL 保持 `/query` 的请求与响应格式不变。
2. THE System SHALL 在启动日志中输出归一化版本重算的开始与完成信息。
3. THE System SHALL 保持历史题库行可读，并在重算后使用新哈希参与精确匹配。
4. THE System SHALL 为选项校验、URL 隔离与第三方精确回写提供单元测试。
5. THE System SHALL 保持只有已知失败 `TestQueryGetAcceptsQAlias` 时其余测试通过。

## 已确认决策

1. 选择题答案以选项文字入库，字母答案会映射为对应选项文字。
2. 第三方近似匹配可以服务当前请求，但不允许回写与缓存。
3. 图片题以 URL 集合区分，不同图片不共享哈希与答案。
4. 归一化算法升级到版本 3，升级后启动会重算一次全库 `QuestionHash`。
