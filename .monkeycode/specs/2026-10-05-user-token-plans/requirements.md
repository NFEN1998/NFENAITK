# Requirements Document

## Introduction

为 nfentik-go 增加「普通用户」体系。目前控制台仅支持单一 `adminToken` 管理登录，所有 `/query` 调用不区分调用者。本需求引入由管理员在后台生成的用户令牌（`usertoken`），用户凭该令牌登录面向普通用户的页面，查看剩余次数/有效期、复制 OCS 配置、查看自己的调用记录、修改自己的令牌，并在到期或用尽时收到提醒。

同时引入套餐（plan）体系，支持两类套餐并存：

- 时长套餐：包月、包季、半年、包年、无限（按有效期计）。
- 次数套餐：固定总调用次数，用尽为止。

计费口径：每次 `/query` 调用扣减 1 次（命中题库或缓存同样扣减）。

## Glossary

- **User**：由管理员创建、持有唯一 `usertoken` 的普通用户。
- **usertoken**：用户登录与调用鉴权的令牌，由管理员生成，全局唯一。
- **Plan**：套餐，定义用户的配额类型与额度。
- **Duration Plan**：时长套餐，按到期时间控制有效期，套餐为「包月/包季/半年/包年」时有效期为对应时长，为「无限」时到期时间为空且不限制调用次数。
- **Count Plan**：次数套餐，拥有固定总次数配额，每次 `/query` 扣减 1 次。
- **Quota**：用户当前套餐的剩余可用量，时长套餐为剩余有效期，次数套餐为剩余次数。
- **Admin**：持有 `adminToken` 的管理员，可使用控制台全部管理能力。
- **User Console**：面向普通用户的前端页面，独立于管理员控制台。
- **OCS Config**：OCS 浏览器插件使用的查询配置对象，包含查询地址、请求方式与数据模板。

## Requirements

### Requirement 1：管理员创建用户并生成令牌

**User Story:** AS 管理员, I want 在控制台创建普通用户并自动生成 usertoken, so that 我可以把令牌分发给用户使用。

#### Acceptance Criteria

1. WHEN 管理员提交创建用户请求且指定用户名与套餐, THE System SHALL 生成唯一 `usertoken` 并创建用户记录。
2. WHEN 管理员提交创建用户请求且未指定套餐, THE System SHALL 拒绝创建并返回错误信息「请选择套餐」。
3. WHEN 系统生成 `usertoken`, THE System SHALL 保证该令牌在所有现存用户中唯一。
4. WHEN 用户创建成功, THE System SHALL 返回用户名、`usertoken`、套餐类型、生效起始时间与到期时间或剩余次数。
5. WHILE 用户已存在, THE System SHALL 支持管理员重置该用户的 `usertoken`。
6. WHEN 管理员重置某个用户的 `usertoken`, THE System SHALL 立即使旧令牌失效。
7. IF 管理员以重复的用户名创建用户, THE System SHALL 拒绝创建并返回错误信息「用户名已存在」。

### Requirement 2：套餐类型与额度

**User Story:** AS 管理员, I want 为每个用户选择时长或次数套餐, so that 我能按不同售卖方式管理配额。

#### Acceptance Criteria

1. THE System SHALL 提供时长套餐类型：包月、包季、半年、包年、无限、自定义时长。
2. THE System SHALL 提供次数套餐类型：固定总次数。
3. WHEN 管理员为时长套餐设置生效时间, THE System SHALL 依据套餐时长计算到期时间，包月为 30 天、包季为 90 天、半年为 180 天、包年为 365 天。
4. WHEN 用户套餐为「无限」, THE System SHALL 将该用户的到期时间置空且不限制调用次数。
5. WHEN 用户套餐为次数套餐, THE System SHALL 在创建时记录总次数与剩余次数，剩余次数初始值等于总次数。
6. WHEN 管理员创建次数套餐用户, THE System SHALL 允许管理员指定总次数且总次数大于 0。
7. IF 管理员为次数套餐指定的总次数小于等于 0, THE System SHALL 拒绝创建并返回错误信息「总次数必须大于 0」。
8. WHEN 用户套餐为「自定义时长」, THE System SHALL 依据管理员输入的天数计算到期时间，天数以自然日累加。
9. IF 管理员为自定义时长套餐输入的天数小于等于 0, THE System SHALL 拒绝创建并返回错误信息「天数必须大于 0」。

### Requirement 3：用户令牌登录

**User Story:** AS 普通用户, I want 使用 usertoken 登录用户页, so that 我能查看我的套餐信息与配置。

#### Acceptance Criteria

1. WHEN 用户在用户页提交有效 `usertoken`, THE System SHALL 建立登录会话并返回该用户的套餐信息。
2. WHEN 用户提交无效或已失效的 `usertoken`, THE System SHALL 拒绝登录并返回错误信息「令牌无效」。
3. WHEN 用户提交令牌为空, THE System SHALL 拒绝登录并返回错误信息「请输入令牌」。
4. WHILE 用户已登录, THE User Console SHALL 允许用户在该会话内访问其套餐信息与调用记录。
5. WHEN 用户主动登出, THE User Console SHALL 清除本地会话令牌并返回登录界面。

### Requirement 4：调用鉴权与扣减

**User Story:** AS 管理员, I want 每次 /query 调用按用户令牌鉴权并扣减配额, so that 配额能够被准确执行。

#### Acceptance Criteria

1. WHEN `/query` 请求携带有效 `usertoken`, THE System SHALL 允许查询并在查询完成后扣减该用户 1 次配额。
2. WHEN 用户套餐为时长套餐且在有效期内, THE System SHALL 允许查询，且时长的到期时间不因查询而改变。
3. WHEN 用户套餐为次数套餐且剩余次数大于 0, THE System SHALL 允许查询并将剩余次数减 1。
4. IF 用户套餐为次数套餐且剩余次数等于 0, THE System SHALL 拒绝查询并返回错误信息「次数已用尽」。
5. IF 用户套餐为时长套餐且当前时间超过到期时间, THE System SHALL 拒绝查询并返回错误信息「套餐已过期」。
6. IF `/query` 请求携带的 `usertoken` 无效或不存在, THE System SHALL 拒绝查询并返回错误信息「令牌无效」。
7. IF `/query` 请求未携带 `usertoken` 且系统开启了「仅令牌用户可用」模式, THE System SHALL 拒绝查询并返回错误信息「缺少令牌」。
8. WHEN 用户套餐为「无限」, THE System SHALL 允许查询且不改变配额。
9. WHEN 查询因配额不足被拒绝, THE System SHALL 不扣减该用户的配额。

### Requirement 5：用户页展示套餐与提醒

**User Story:** AS 普通用户, I want 查看剩余次数与到期时间并收到提醒, so that 我能及时续费或补充次数。

#### Acceptance Criteria

1. WHEN 用户登录成功, THE User Console SHALL 展示用户名、套餐类型、生效起始时间、剩余有效期或剩余次数。
2. WHILE 用户套餐为时长套餐且剩余有效期小于等于 7 天, THE User Console SHALL 以醒目样式提示即将到期。
3. WHILE 用户套餐为次数套餐且剩余次数小于等于总次数的 20%, THE User Console SHALL 以醒目样式提示次数即将用尽。
4. IF 用户套餐已过期或次数已用尽, THE User Console SHALL 在页面顶部显示不可用提示。

### Requirement 6：复制 OCS 配置

**User Story:** AS 普通用户, I want 一键复制属于我的 OCS 配置, so that 我能直接粘贴到浏览器插件中使用。

#### Acceptance Criteria

1. WHILE 用户已登录, THE User Console SHALL 提供「复制 OCS 配置」按钮。
2. WHEN 用户点击「复制 OCS 配置」, THE User Console SHALL 将包含用户 `usertoken` 的 OCS 配置写入剪贴板。
3. THE OCS Config SHALL 包含查询地址、请求方式、内容类型与包含 `usertoken` 的数据模板。
4. WHEN 剪贴板写入失败, THE User Console SHALL 以回退方式提供可手动复制的配置文本。

### Requirement 7：用户查看自己的调用记录

**User Story:** AS 普通用户, I want 查看自己的调用记录, so that 我能确认配额消耗情况。

#### Acceptance Criteria

1. WHILE 用户已登录, THE User Console SHALL 展示该用户最近的调用记录。
2. THE 调用记录 SHALL 包含时间、问题、是否命中题库或 AI 与状态。
3. WHILE 用户已登录, THE User Console SHALL 仅展示属于当前登录用户的调用记录。
4. THE System SHALL 支持对调用记录按时间倒序分页展示。
5. THE System SHALL 对每个用户的调用记录保留最近 200 条。
6. THE System SHALL 对调用记录保留最近 30 天。
7. THE System SHALL 将全部用户的调用记录总量限制在 50000 条以内。
8. WHEN 调用记录超过保留条数、保留天数或总量上限, THE System SHALL 删除最旧的记录。

### Requirement 8：用户修改自己的令牌

**User Story:** AS 普通用户, I want 重置自己的 usertoken, so that 令牌泄漏时我可以使其失效。

#### Acceptance Criteria

1. WHILE 用户已登录, THE User Console SHALL 提供「重置令牌」操作。
2. WHEN 用户确认重置令牌, THE System SHALL 生成新的 `usertoken` 并立即使旧令牌失效。
3. WHEN 令牌重置成功, THE User Console SHALL 更新本地会话令牌并在 OCS 配置中反映新令牌。
4. IF 用户未确认重置操作, THE System SHALL 保持原令牌不变。

### Requirement 9：管理员续费与调整套餐

**User Story:** AS 管理员, I want 为用户续费或变更套餐, so that 我能应对用户续费与套餐变更。

#### Acceptance Criteria

1. WHEN 管理员为用户延长套餐, THE System SHALL 依据所选套餐类型更新到期时间或剩余次数。
2. WHEN 管理员为次数套餐用户增加次数, THE System SHALL 将新增次数累加到剩余次数。
3. WHEN 管理员将用户的套餐类型切换为时长套餐, THE System SHALL 清除剩余次数并按新套餐设置到期时间。
4. WHEN 管理员将用户的套餐类型切换为次数套餐, THE System SHALL 清除到期时间并按总次数重置剩余次数。
5. WHILE 管理员处于用户管理视图, THE User Management SHALL 展示每个用户的用户名、套餐类型、状态（有效、即将过期、已过期、次数用尽）与剩余配额。

### Requirement 10：会话与安全

**User Story:** AS 管理员, I want 用户令牌具备基本安全约束, so that 令牌不被滥用。

#### Acceptance Criteria

1. WHEN 系统生成 `usertoken`, THE System SHALL 使用密码学安全随机源生成足够长度的令牌。
2. WHILE 用户已登录, THE User Console SHALL 在服务端校验用户身份后才返回该用户数据。
3. IF 请求的 `usertoken` 与所访问资源所属用户不一致, THE System SHALL 拒绝访问并返回错误信息「无权访问」。
4. THE System SHALL 在用户列表中仅向管理员展示 `usertoken` 的完整值。
