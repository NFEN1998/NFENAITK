# Requirements Document

## Introduction

为 nfentik-go 的题库导入增加一种「直连 MOGO 数据库」的采集渠道。当前 MOGO 采集走其内部 HTTP 接口（`/getQuestionHeader` + `/getData`），在数据量大或 MOGO 主机繁忙时会返回 `502/503/504`，导入被拖慢甚至中断。

本需求允许管理员配置一个到 MOGO 主机数据库（MySQL/MariaDB）的连接，直接按主键游标分批读取题目表，绕过 HTTP 接口。该渠道作为现有「MOGO 源」的一种类型（与 HTTP 源并列），复用现有导入任务的调度（手动/间隔/每天定点）、增量游标、限速、互斥与进度展示，并新增可配置的读取速度控制，以便在不影响 MOGO 正常运行的前提下尽快把题库同步到本地。

目标：让管理员无需改代码即可用数据库直连方式采集 MOGO 题库，读取速度可配、可控、可断点续采。

## Glossary

- **MOGO Source（MOGO 源）**：一条导入配置，类型为 HTTP 或数据库，两者在控制台同一列表中管理。
- **HTTP Source（HTTP 源）**：现有类型，通过 MOGO 内部 HTTP 接口采集。
- **Database Source（数据库源）**：新增类型，通过数据库协议直连 MOGO 数据库采集。
- **Direct Read（直连读取）**：nfentik-go 通过数据库协议连接 MOGO 数据库并按游标读取题目行。
- **Cursor（游标）**：本次已读取到的题目主键值（`id`），用于断点续采与增量判断。
- **Incremental（增量模式）**：仅导入主键大于上次游标的题目行。
- **Backfill（回填模式）**：从游标处继续读取全部题目行直至表尾。
- **Rows Per Second（每秒行数）**：读取限速的表达，即每秒最多读取的行数。
- **Batch（批次）**：一次从数据库读取的行集合，批次大小可配置。
- **Column Mapping（列映射）**：把 MOGO 题目表的列映射为本地字段（主键、题目、答案、选项、题型），默认值为 `id/question/answer/opt/questionType`。
- **Read-only Connection（只读连接）**：对 MOGO 数据库仅执行 `SELECT`，不写入、不修改任何数据。
- **Overwrite（覆盖已有答案）**：数据库源的可选开关；开启后，导入行会替换同题同选项已有行的答案与来源。
- **AppSettings**：数据库中的系统设置表，MOGO 源配置以 JSON 形式持久化于其中。
- **Admin**：持有 `adminToken` 的管理员。

## Requirements

### Requirement 1：配置 MOGO 数据库源

**User Story:** AS 管理员, I want 在控制台增删改查 MOGO 数据库源, so that 我无需改代码即可直连采集题库。

#### Acceptance Criteria

1. THE System SHALL 在 MOGO 源配置中支持类型字段，取值为 `http` 或 `database`。
2. THE System SHALL 为每个数据库源持久化字段：显示名称、类型、主机、端口、用户名、密码、数据库名、题目表名、列映射、批次大小、限速配置、重试次数、调度配置与游标。
3. WHEN 管理员保存数据库源, THE System SHALL 校验显示名称、主机、用户名、数据库名与题目表名非空。
4. IF 管理员保存的端口不在 1 到 65535 之间, THE System SHALL 拒绝保存并返回错误信息「端口无效」。
5. IF 管理员保存的批次大小不在 1 到 5000 之间, THE System SHALL 将批次大小收敛到该范围内。
6. WHERE 管理员未填写列映射, THE System SHALL 使用默认映射 `id/question/answer/opt/questionType`。
7. THE System SHALL 在控制台与接口响应中对数据库源的密码进行脱敏，仅返回占位符；WHEN 管理员保存时提交脱敏占位符, THE System SHALL 保留原已保存的密码。
8. WHEN 管理员提交连接测试, THE System SHALL 使用所填连接信息建立连接并读取题目表结构，返回可供映射的列名列表或错误原因。

### Requirement 2：直连读取题库

**User Story:** AS 管理员, I want 直连读取 MOGO 数据库中的题目, so that 采集更快更稳定。

#### Acceptance Criteria

1. WHEN 一个数据库源开始运行, THE System SHALL 建立到该数据库的只读连接并按主键游标升序分批读取题目行。
2. THE System SHALL 仅对 MOGO 数据库执行 `SELECT` 语句，不执行任何数据修改语句。
3. WHEN 数据库源处于增量模式, THE System SHALL 仅导入主键大于上次游标的题目行。
4. WHEN 数据库源处于回填模式, THE System SHALL 从游标处继续读取直至表尾。
5. WHEN 一行的题目或答案为空, THE System SHALL 跳过该行且不计入已导入。
6. WHEN 一行已存在于本地题库（按归一化题目与选项）, THE System SHALL 跳过该行并计入已跳过。
7. THE System SHALL 按列映射把数据库行的主键、题目、答案、选项与题型转换为本地字段后写入本地题库。
8. THE System SHALL 复用 MOGO 现有的选项解析与题目拆分规则处理数据库行的题目与选项文本。

### Requirement 3：读取速度控制

**User Story:** AS 管理员, I want 控制数据库读取速度, so that 采集不影响 MOGO 的正常运行。

#### Acceptance Criteria

1. THE System SHALL 支持为每个数据库源配置批次大小。
2. THE System SHALL 支持为每个数据库源配置读取限速，限速以「每秒读取的最大行数」表达。
3. WHILE 已配置每秒最大行数大于 0, THE System SHALL 使实际读取速率不超过该值。
4. THE System SHALL 支持为每个数据库源配置批间延迟毫秒数，在每批读取之间休眠指定时长。
5. WHILE 未配置每秒行数且未配置批间延迟, THE System SHALL 以批次大小连续读取。
6. WHEN 管理员保存数据库源且每秒行数为负, THE System SHALL 将每秒行数收敛为 0。
7. THE System SHALL 支持为每个数据库源配置单次运行的最大导入行数，达到上限时结束本次运行并保留游标。

### Requirement 4：与现有导入任务集成

**User Story:** AS 管理员, I want 数据库采集沿用现有导入能力, so that 调度、进度与断点续采的行为保持一致。

#### Acceptance Criteria

1. THE System SHALL 复用现有的导入任务互斥机制，同一时刻仅运行一个导入任务（HTTP 源或数据库源）。
2. THE System SHALL 为数据库源提供与 HTTP 源一致的调度配置（手动、间隔分钟、每天定点）。
3. WHILE 数据库源运行中, THE System SHALL 展示进度：已导入、已跳过、已扫描、当前游标、题库总数与错误信息。
4. WHEN 数据库源运行结束（正常或失败）, THE System SHALL 持久化游标、上次运行时间、上次导入数量与错误信息。
5. WHEN 数据库源运行结束且本次有新增, THE System SHALL 一次性失效查询缓存。
6. WHEN 管理员停止运行中的数据库源任务, THE System SHALL 终止读取并保留已持久化的游标。
7. THE System SHALL 在控制台以同一列表管理 HTTP 源与数据库源，并为两者分别提供运行、编辑、测试与删除操作。

### Requirement 5：安全与容错

**User Story:** AS 管理员, I want 数据库采集安全且可容错, so that 连接信息不泄露且瞬时故障可恢复。

#### Acceptance Criteria

1. WHEN 读取一行失败且为可重试的数据库错误, THE System SHALL 按配置的重试次数与固定间隔重试该次读取。
2. IF 重试次数用尽仍未成功, THE System SHALL 结束本次运行并记录错误信息，同时保留游标。
3. WHILE 数据库连接不可用, THE System SHALL 不改变查询链路的行为。
4. THE System SHALL 在日志中不输出数据库源密码明文。
5. WHEN 数据库源配置的列映射指向不存在的列, THE System SHALL 结束本次运行并返回包含缺失列名的错误信息。

### Requirement 6：覆盖已有答案（显式开关）

**User Story:** AS 管理员, I want 在需要时让数据库导入覆盖已有答案, so that 权威题库可以替换 AI 或第三方写入的旧答案。

#### Acceptance Criteria

1. THE System SHALL 为每个数据库源持久化布尔字段 `overwrite`，默认值为 `false`。
2. WHILE `overwrite` 为 `false`, THE System SHALL 对已存在的同题同选项行跳过写入并保留该行的答案与来源。
3. WHEN `overwrite` 为 `true` 且同题同选项行已存在, THE System SHALL 用导入行的答案替换该行，并在导入行提供题型时替换题型，清除待修正标记，将 `IsAi` 置为 `false`，并把来源改为 `import:mogo-db`。
4. WHEN `overwrite` 为 `true` 且同题同选项行已存在, THE System SHALL 保持该行已存的题目文本与选项文本不变。
5. WHILE 数据库源运行中, THE System SHALL 在进度中分别统计已导入、已更新、已跳过与已扫描的行数。
6. WHEN 数据库源运行结束, THE System SHALL 持久化本次已更新行数。
7. WHEN 源类型为 `http`, THE System SHALL 将 `overwrite` 置为 `false`。

## 已确认决策

1. MOGO 数据库为 MySQL/MariaDB，可提供只读账号，且从 nfentik-go 服务器可直连。
2. 题目表的列名与 HTTP 接口 `/getQuestionHeader` 返回字段一致，默认映射 `id/question/answer/opt/questionType`。
3. 数据库源作为现有 MOGO 源的一种类型（HTTP/数据库），限速以「每秒行数」为主、「批间延迟毫秒」为辅。
4. 数据库导入写入本地题库的来源标记为 `import:mogo-db`，与 HTTP 导入区分。
5. 增量游标复用现有 `maxId` 字段，按主键升序续采。
6. 新建数据库源默认每秒 100 行、批次 500；批间延迟默认 0，每秒行数设为 0 表示不限速。
7. 覆盖开关默认关闭；开启后替换答案与来源，导入行提供题型时一并替换，题目与选项文本保持不变。长期开启会覆盖后台人工修正的同题答案，建议全量重建时临时开启。
