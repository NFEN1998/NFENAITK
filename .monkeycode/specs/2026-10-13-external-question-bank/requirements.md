# Requirements Document

## Introduction

为 nfentik-go 的 `/query` 查询链路增加「第三方题库接口」来源。当前链路为：Redis 缓存 → 本地题库（精确哈希 + 模糊匹配）→ AI 文本/视觉模型兜底。本需求在「本地题库未命中」与「AI 兜底」之间，插入一个或多个可配置的第三方题库接口源：本地未命中时按优先级依次调用第三方接口，命中即返回，并可选择把答案回写本地题库与缓存；全部未命中再走 AI 兜底。

目标是复用现有模型配置的管理范式（存于数据库 `AppSettings`、控制台可视化编辑、连接信息脱敏），让管理员能够不写代码地接入任意 JSON 形态的第三方题库接口。

## Glossary

- **External Bank（第三方题库）**：外部提供的、通过 HTTP 接口按题目查询答案的服务。
- **Source（题库源）**：一个第三方题库的接入配置，包含地址、请求映射、响应提取、超时、优先级等。
- **Adapter（适配器）**：把统一的查询请求转换为某 Source 的 HTTP 请求、并把其响应解析为答案的组件。
- **Lookup（查询）**：向一个 Source 发起一次第三方题库查询。
- **Write-back（回写）**：把第三方返回的答案写入本地题库（`AIResponses`）与查询缓存。
- **Priority（优先级）**：Source 被调用的先后顺序，数值小者先调用。
- **Verification（校验）**：用本地匹配算法判断第三方返回的题目是否与查询题目等价，以决定是否采信。
- **Admin**：持有 `adminToken` 的管理员。
- **AppSettings**：数据库中的系统设置表，第三方题库源以 JSON 形式持久化于其中。

## Requirements

### Requirement 1：配置第三方题库源

**User Story:** AS 管理员, I want 在控制台增删改查第三方题库源, so that 我无需改代码即可接入外部题库。

#### Acceptance Criteria

1. THE System SHALL 支持管理员创建、编辑、启用、停用与删除第三方题库源。
2. THE System SHALL 为每个题库源持久化字段：显示名称、请求地址、请求方法、请求头、请求体/参数模板、响应答案提取路径、超时秒数、优先级、是否回写。
3. WHEN 管理员保存题库源, THE System SHALL 校验显示名称与请求地址非空。
4. WHILE 存在多个题库源, THE System SHALL 按优先级升序排列调用顺序。
5. IF 管理员保存的题库源请求地址不是合法的 http/https URL, THE System SHALL 拒绝保存并返回错误信息「请求地址无效」。
6. WHEN 管理员提交测试连通性请求, THE System SHALL 使用给定题目向该题库源发起一次查询并返回结果或错误原因。
7. THE System SHALL 在请求日志与响应日志中对题库源的密钥类请求头（如 Authorization、X-Api-Key）做脱敏处理。

### Requirement 2：请求与响应适配

**User Story:** AS 管理员, I want 把题目按目标接口的格式发送并解析其返回, so that 可以对接形态各异的第三方题库。

#### Acceptance Criteria

1. THE System SHALL 支持在请求地址、请求参数与请求体中引用占位符 `{question}`、`{options}`、`{type}`。
2. THE System SHALL 支持 `GET`（query 参数）与 `POST`（JSON 或表单）两种请求方式。
3. WHEN 题库源配置了响应答案提取路径, THE System SHALL 优先按该路径从 JSON 响应中提取答案字符串。
4. WHEN 题库源未配置响应答案提取路径或所配置路径未取到答案, THE System SHALL 自动识别响应中的答案字段，按常见键名自浅至深查找，并支持嵌套对象、数组与数字答案。
5. WHEN 题库源未配置题目回显路径, THE System SHALL 自动识别响应中回显的题目字段以用于答案校验。
6. IF 响应中不存在可识别的答案字段, THE System SHALL 将该次查询视为未命中。
7. IF 第三方响应不是合法 JSON, THE System SHALL 将该次查询视为未命中并记录原因。

### Requirement 3：查询链路接入

**User Story:** AS 使用者, I want 本地题库未命中时自动查询第三方题库, so that 得到答案的概率更高。

#### Acceptance Criteria

1. WHEN 一个 `/query` 请求在缓存与本地题库中均未命中且存在已启用的题库源, THE System SHALL 按优先级顺序调用题库源。
2. WHEN 某个题库源返回了非空且通过校验的答案, THE System SHALL 立即返回该答案并停止调用后续题库源。
3. WHEN 全部题库源均未命中或调用失败, THE System SHALL 继续执行 AI 兜底流程。
4. WHILE 题库源全部停用, THE System SHALL 不改变原有缓存 → 本地题库 → AI 的链路行为。
5. WHEN 查询题目或选项包含 URL, THE System SHALL 按现有规则跳过缓存并对第三方题库源与视觉模型保持相同的处理方式。

### Requirement 4：结果回写与缓存

**User Story:** AS 管理员, I want 第三方命中的答案沉淀到本地, so that 后续相同题目无需再次请求外部。

#### Acceptance Criteria

1. WHEN 某题库源标记为启用回写且返回了采信答案, THE System SHALL 将该题目与答案写入本地题库。
2. WHEN 回写时本地题库已存在等价的题目, THE System SHALL 复用已有记录而不新增重复行。
3. WHEN 题库源返回的答案被采信, THE System SHALL 将该结果写入查询缓存。
4. THE System SHALL 以题目与选项的归一化键作为缓存键，与现有缓存键保持一致。
5. WHILE 题库源被标记为不回写, THE System SHALL 仅在本次查询返回其答案而不写入本地题库。

### Requirement 5：答案校验与防污染

**User Story:** AS 管理员, I want 校验第三方返回结果, so that 不相关或低质量答案不会污染本地题库。

#### Acceptance Criteria

1. WHEN 题库源返回的响应包含题目文本, THE System SHALL 使用本地匹配算法计算该题目与查询题目的相似度。
2. IF 题库源返回题目的相似度低于阈值（默认 0.72）, THE System SHALL 丢弃该答案并在本次查询中视为未命中。
3. IF 题库源返回的答案文本为空或仅含空白字符, THE System SHALL 将其视为未命中。
4. WHILE 题库源被配置为信任模式, THE System SHALL 跳过相似度校验直接采用非空答案。
5. THE System SHALL 复用现有的不可信答案识别逻辑，拒绝把无效占位答案写入本地题库。

### Requirement 6：错误处理与可观测

**User Story:** AS 管理员, I want 第三方故障不影响整体可用性, so that 单个外部接口异常不会导致查询失败。

#### Acceptance Criteria

1. WHEN 某题库源请求超时或返回错误状态码, THE System SHALL 记录该错误并继续调用下一个题库源。
2. IF 全部题库源调用失败, THE System SHALL 继续执行 AI 兜底流程。
3. THE System SHALL 为每个题库源设置可配置的超时时间，默认 10 秒。
4. WHEN 一次查询由某题库源命中, THE System SHALL 在请求日志与用户使用记录中把来源标记为该题库源的名称。
5. THE System SHALL 在数据库不可用导致回写失败时，仍向调用方返回已采信的答案。

## Requirements Traceability

| Requirement | 涉及组件 |
| --- | --- |
| R1 配置题库源 | 控制台设置接口、AppSettings 存储、Web 控制台 |
| R2 请求与响应适配 | `internal/externalbank` 适配器 |
| R3 查询链路接入 | `internal/server/query.go` 解析链路 |
| R4 回写与缓存 | 本地题库存储、Redis 查询缓存 |
| R5 校验与防污染 | 本地匹配包 `internal/match`、答案校验 |
| R6 错误处理与可观测 | 请求日志、用户使用记录、超时控制 |
