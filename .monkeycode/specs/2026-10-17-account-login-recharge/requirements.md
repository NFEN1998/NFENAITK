# Requirements Document

## Introduction

为 nfentik-go 的用户中心增加账号体系与卡密充值，同时保留现有 usertoken 作为 OCS 查询凭据。账号用于网页端身份与充值，token 用于设备端调用，二者一对一绑定。新注册账号自动获得一个零额度 token；存量 token 用户可以在用户中心绑定账号；用户通过卡密给当前账号充值。登录页默认保留 token 登录，账号登录作为可选方式。

目标：让用户不依赖管理员人工续费，能够用账号登录、绑定或获取 token，并通过卡密自助恢复额度。

## Glossary

- **Account（账号）**：用户中心的登录身份，由用户名与密码组成，绑定唯一 usertoken。
- **Token（令牌）**：现有 usertoken，作为 OCS 与 `/query` 的调用凭据。
- **Binding（绑定）**：把账号与一个已有 token 的用户行关联；一个账号绑定一个 token，一个 token 最多绑定一个账号。
- **Redemption Code（卡密）**：管理员批量生成的一次性充值码，包含次数或时长额度。
- **Zero-quota Account（零额度账号）**：次数套餐且总次数、剩余次数、已用次数均为 0 的新注册账号。
- **Session（会话）**：账号登录后由服务端保存、通过 HttpOnly cookie 传递的登录状态。
- **Default Login（默认登录）**：用户中心登录页默认展示的令牌登录方式。
- **Plan Mismatch（套餐不匹配）**：卡密类型与用户当前套餐类型不一致，导致不能直接充值的情况。

## Requirements

### Requirement 1：账号注册

**User Story:** AS 新用户, I want 注册账号并自动获得 token, so that 我可以登录用户中心并在充值后使用 OCS。

#### Acceptance Criteria

1. WHILE 注册开关为开启, THE System SHALL 在用户中心提供用户名与密码注册。
2. WHEN 用户提交注册, THE System SHALL 校验用户名长度为 3 到 32 个字符、由字母数字下划线组成且以字母开头。
3. WHEN 用户提交注册, THE System SHALL 校验密码长度为 8 到 72 个字节。
4. IF 用户名在忽略大小写后已存在, THE System SHALL 拒绝注册并返回「用户名已被占用」。
5. WHEN 注册请求未携带 token 且注册成功, THE System SHALL 创建一个零额度次数套餐用户并生成唯一 token。
6. WHEN 注册请求携带有效 token 且注册成功, THE System SHALL 把新账号绑定到该 token 对应的用户，并保持 token 不变。
7. IF 注册请求携带的 token 无效或已绑定账号, THE System SHALL 拒绝注册并返回「令牌无效或已被绑定」。
8. WHILE 注册开关为关闭, THE System SHALL 拒绝注册并返回「注册暂未开放」。
9. IF 同一 IP 在一小时内提交超过 5 次注册, THE System SHALL 拒绝后续注册并返回「注册过于频繁」。
10. WHEN 注册成功, THE System SHALL 创建登录会话并直接进入用户中心。

### Requirement 2：账号登录与会话

**User Story:** AS 已注册用户, I want 用账号密码登录用户中心, so that 我不需要每次输入长 token。

#### Acceptance Criteria

1. WHEN 用户提交正确的用户名与密码, THE System SHALL 创建服务端会话并设置 HttpOnly 会话 cookie。
2. IF 用户名不存在或密码错误, THE System SHALL 返回统一提示「用户名或密码错误」。
3. THE System SHALL 将会话有效期默认设为 30 天，并在每次请求时刷新最后活跃时间。
4. THE System SHALL 使用 `HttpOnly`、`SameSite=Lax` 的会话 cookie，并在 HTTPS 请求下附加 `Secure`。
5. WHEN 用户退出登录, THE System SHALL 立即撤销当前会话。
6. WHEN 管理员重置密码或停用账号, THE System SHALL 撤销该账号的全部会话。
7. IF 同一用户名在 15 分钟内连续失败 5 次登录, THE System SHALL 暂时拒绝该用户名登录并返回「登录过于频繁，请稍后再试」。
8. WHILE 用户打开用户中心登录页, THE System SHALL 默认展示令牌登录，并允许切换到账号登录。
9. THE System SHALL 保持现有令牌登录接口与响应行为兼容。

### Requirement 3：令牌绑定与展示

**User Story:** AS 已有 token 的用户, I want 为现有 token 绑定账号, so that 我可以改用账号登录并自助充值。

#### Acceptance Criteria

1. WHEN 用户以有效 token 登录用户中心且尚未绑定账号, THE System SHALL 展示「绑定账号」入口。
2. WHEN 用户提交合法的用户名与密码, THE System SHALL 把账号绑定到当前 token 对应的用户。
3. IF 当前 token 已绑定账号, THE System SHALL 拒绝再次绑定并返回「该令牌已绑定账号」。
4. IF 提交的用户名已被占用, THE System SHALL 拒绝绑定并返回「用户名已被占用」。
5. THE System SHALL 在绑定过程中保持 token 值不变。
6. WHEN 账号绑定完成, THE System SHALL 允许用户使用账号密码登录并看到同一个 token。
7. WHEN 管理员解除绑定, THE System SHALL 保留用户与 token，把账号置为未绑定状态并拒绝该账号登录。
8. THE System SHALL 在账号中心展示完整 token，并提供复制操作。

### Requirement 4：卡密充值

**User Story:** AS 用户, I want 用卡密给当前账号充值, so that token 用完后可以继续使用。

#### Acceptance Criteria

1. WHEN 管理员创建卡密批次, THE System SHALL 接受卡密类型、每次充值额度、生成数量与兑换截止时间。
2. THE System SHALL 为每个卡密生成不可预测的随机码，并只在创建时展示一次明文。
3. THE System SHALL 在数据库中只保存卡密明文的 SHA-256 摘要。
4. WHEN 已登录用户提交卡密, THE System SHALL 把额度充值到该用户自己的账号。
5. WHEN 次数卡密被兑换, THE System SHALL 把卡密额度累加到用户的 `TotalCount` 与 `RemainCount`。
6. WHEN 时长卡密被兑换, THE System SHALL 从「当前到期时间」与「当前时间」中较晚者起顺延卡密对应的天数。
7. WHEN 零额度账号兑换任一类型卡密, THE System SHALL 把账号切换为该卡密对应的套餐类型并写入额度。
8. IF 用户当前为未到期的时长套餐且兑换次数卡密, THE System SHALL 拒绝兑换并返回「当前套餐请使用时长卡密」。
9. IF 用户当前为有剩余次数的次数套餐且兑换时长卡密, THE System SHALL 拒绝兑换并返回「当前套餐请使用次数卡密」。
10. IF 卡密已使用、已过期或已作废, THE System SHALL 拒绝兑换并返回「卡密无效或已使用」。
11. WHEN 多个请求同时兑换同一卡密, THE System SHALL 只允许一个请求成功。
12. THE System SHALL 为每次成功兑换写入用户审计日志，记录操作者、卡密批次、额度与时间。
13. IF 同一账号在一小时内失败兑换超过 10 次, THE System SHALL 暂时拒绝该账号继续兑换。
14. WHEN 管理员作废未使用的卡密, THE System SHALL 阻止该卡密后续兑换。

### Requirement 5：用户中心界面

**User Story:** AS 普通用户, I want 在用户中心完成登录、注册、绑定与充值, so that 我不需要联系管理员处理额度。

#### Acceptance Criteria

1. THE System SHALL 在登录页提供「令牌登录」与「账号登录」两个入口，并默认选中令牌登录。
2. THE System SHALL 提供注册入口，注册表单包含用户名、密码、确认密码与可选 token。
3. WHEN 用户以账号登录, THE System SHALL 在刷新页面后保持登录状态。
4. THE System SHALL 在账号中心展示用户名、token、套餐、状态、剩余额度、调用记录与充值入口。
5. WHILE 用户为零额度或次数用尽, THE System SHALL 展示「请兑换卡密或购买套餐」的提示。
6. WHILE 用户以 token 登录且未绑定账号, THE System SHALL 在账号中心展示绑定入口。
7. THE System SHALL 提供修改密码、退出登录与复制 token 的操作。
8. WHEN 用户兑换卡密成功, THE System SHALL 立即刷新并展示新的套餐与剩余额度。

### Requirement 6：管理端

**User Story:** AS 管理员, I want 管理账号与卡密, so that 我可以支持用户注册、充值与账号问题处理。

#### Acceptance Criteria

1. THE System SHALL 在管理控制台提供账号列表，展示用户名、绑定 token、套餐状态、最近登录时间，并支持按用户名搜索。
2. THE System SHALL 支持管理员重置账号密码、停用账号、启用账号、解除 token 绑定、重新绑定 token 与撤销会话。
3. THE System SHALL 支持管理员创建卡密批次、查看批次、在创建时下载卡密明文与作废未使用卡密。
4. THE System SHALL 在系统设置中提供注册开关、会话有效期与充值限流参数。
5. THE System SHALL 为账号创建、绑定、解绑、密码重置、停用、卡密生成、卡密作废与卡密兑换写入审计日志。

### Requirement 7：兼容与迁移

**User Story:** AS 现有部署, I want 升级后继续使用原 token 与查询链路, so that 已有用户不受影响。

#### Acceptance Criteria

1. THE System SHALL 保持所有现有 token 原值可用，升级不改变 token。
2. THE System SHALL 通过幂等迁移新增账号、会话、卡密批次与卡密表，并保持 MySQL 与 PostgreSQL 均可运行。
3. THE System SHALL 保持 `/query` 的鉴权方式与响应格式不变。
4. THE System SHALL 仅在用户中心与管理控制台使用账号会话，OCS 调用继续使用 token。
5. THE System SHALL 允许存量用户在任意时间绑定账号，不强制迁移。
6. THE System SHALL 保持管理员 `adminToken` 的登录与鉴权行为不变。

### Requirement 8：安全与防滥用

**User Story:** AS 运营者, I want 账号与充值流程安全可控, so that 用户凭据与额度不被滥用。

#### Acceptance Criteria

1. THE System SHALL 使用 bcrypt 或 argon2id 保存密码摘要，并禁止在日志与接口响应中输出密码明文。
2. THE System SHALL 对注册、登录与卡密兑换实施独立的频率限制。
3. THE System SHALL 对账号登录失败返回统一错误信息，避免泄露用户名是否存在。
4. THE System SHALL 对使用会话 cookie 的写操作校验 CSRF token。
5. THE System SHALL 在绑定 token 时要求调用方持有该 token。
6. THE System SHALL 在数据库中只保存卡密摘要与用户密码摘要。
7. THE System SHALL 在登录、绑定、改密、充值与解绑时写入审计日志。
8. WHEN 用户或管理员重置密码, THE System SHALL 撤销该账号的全部会话。
9. THE System SHALL 在部署文档中建议通过 HTTPS 提供用户中心。

## 已确认决策

1. 账号与 token 采用一对一绑定；本次不实现一个账号绑定多个 token。
2. 新注册账号的初始额度为 0，注册后需兑换卡密或购买套餐才能查询。
3. 第一版充值渠道为卡密兑换，本次不接入在线支付。
4. 登录页默认保留令牌登录；账号登录为可选入口。
5. 卡密同时支持次数与时长；套餐类型不匹配时拒绝兑换，不做隐式套餐切换或额度清零。
6. 注册初期不要求邮箱或手机验证；通过注册开关、IP 限流与零额度控制滥用。
7. 账号登录使用服务端会话 cookie；token 继续作为 OCS 查询凭据。
