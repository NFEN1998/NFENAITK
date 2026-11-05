# nfentik-go 接口文档

nfentik-go 对外提供三类 HTTP 接口：面向 OCS 用户脚本的查询接口、面向普通用户的用户接口、面向管理员的管理接口。认证通过请求头 `Authorization: Bearer <token>` 或查询参数/请求体携带令牌。

## 认证

| 接口 | 凭据 | 说明 |
| --- | --- | --- |
| `/query`、`/nfentk`、`/api/model/*` | 可选 `usertoken`（`/nfentk` 也接受 `key`） | 是否强制由设置 `requireTokenForQuery` 决定；`usersEnabled` 关闭时不校验 |
| `/api/user/*` | `usertoken` | 普通用户令牌，前缀通常为 `nf_` |
| `/api/admin/*` | `adminToken` | 管理员令牌；当 `adminToken` 为空时管理接口不校验 |

## OCS 查询接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / HEAD | `/` | 根路径，`HEAD` 返回 `Hello,OCS` 并广播事件 |
| GET / POST | `/query` | 题库查询（核心接口，URL 模式2） |
| GET | `/nfentk` | MOGO 题库对接（URL 模式1） |
| GET | `/api/status` | 服务状态 |
| GET | `/api/time` | 服务器时间 |
| POST | `/api/echo` | 回显请求体 |
| POST | `/api/questions/{id}/pending-correction` | 标记题目为待修正 |
| POST | `/api/model/response` | 前端回传模型调用结果 |
| POST | `/api/model/progress` | 前端上报模型流式进度 |
| GET | `/api/logs/stream` | 请求日志 SSE 流 |
| POST | `/api/login` | 校验管理员/用户令牌 |
| GET | `/api/models` | 读取模型配置（需管理员令牌） |
| POST | `/api/model/call` | 单模型调用，返回完整结果 |
| POST | `/api/model/stream` | 单模型调用，SSE 逐 token 返回 |

### GET / POST `/query`

请求体（POST，`application/json`）：

```json
{
  "title": "题目内容",
  "options": "A. ... B. ...",
  "type": "single_choice",
  "raw": false,
  "token": "nf_xxx"
}
```

GET 形式接受查询参数 `title`（别名 `q`）、`options`、`type`、`raw`、`token`。GET 参数会保留数学表达式中的 `+`（例如 `q=1+1=` 按 `1+1=` 处理），普通文本中的 `+` 仍按空格解码。

公式归一化保留小数点、比例、阶乘、根号、绝对值、上标、括号和非结尾等号；等价运算符 `×`/`*`、`÷`/`/`、`≤`/`<=`、`≠`/`!=`、`−`/`-` 在模糊匹配中按等价处理。客户端发送 `&`、`#`、`%` 时需编码为 `%26`、`%23`、`%25`。

响应：

```json
{
  "code": 1,
  "data": {
    "id": 12,
    "question": "题目内容（含标记为待修正按钮）",
    "answer": "答案",
    "is_ai": true,
    "is_pending_correction": false
  }
}
```

`code` 为 `1` 表示成功、`0` 表示失败；失败时 `message` 字段说明原因。查询解析顺序为：Redis 缓存 → 本地题库（精确哈希 + 模糊匹配）→ 第三方题库链 → AI 模型。来源内部标记为 `cache` / `bank` / `external:<name>` / `ai`，并写入用户使用记录。

### GET `/nfentk`

MOGO 题库对接接口（URL 模式1），供 MOGO 的题库设置或外置接口调用。它复用 `/query` 的查询链、令牌校验与配额，只改变入参解析和返回结构。

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `q` | 是 | 题目内容；兼容 `title` 兜底 |
| `info` | 否 | `题型****载荷`，例如 `多选题****[选项1###选项2]` |
| `kcinfo` | 否 | 课程信息 `课程名\|课程id\|账号`，接收后忽略 |
| `key` / `token` | 否 | 用户令牌，`key` 与 `token` 等价；是否必填由设置决定 |

`info` 的题型支持：单选题、多选题、判断题、填空题、简答题、名词解释、论述题、计算题、口语题、资料题、问答题、连线题、排序题、完型填空/完形填空、学习通讨论、智慧树互动。选项分隔符按 `###` 解析，同时兼容 `|||`。

请求示例：

```
https://<域名>/nfentk?key=nf_xxx&q=题目内容&info=多选题****[选项1###选项2###选项3]&kcinfo=课程名|123|账号
```

成功响应，`data` 为字符串，多答案使用 `###` 连接：

```json
{"code":1,"data":"选项1###选项3"}
```

失败响应：

```json
{"code":-1}
```

`/nfentk` 的应用层失败统一返回 HTTP 200 + `code:-1`；`/query` 的请求与返回结构保持不变。

## 用户接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/user/login` | 用户令牌登录 |
| POST | `/api/user/register` | 注册账号；可选携带已有 token 绑定 |
| POST | `/api/user/account/login` | 账号密码登录，返回 HttpOnly 会话 cookie |
| POST | `/api/user/account/logout` | 退出并撤销账号会话 |
| GET | `/api/user/account/me` | 当前账号与绑定信息 |
| POST | `/api/user/account/bind` | 为当前令牌绑定账号 |
| POST | `/api/user/account/password` | 修改账号密码并轮换会话 |
| POST | `/api/user/redeem` | 兑换卡密给当前用户充值 |
| GET | `/api/user/me` | 当前用户、双权益与套餐目录（支持 token 或账号会话） |
| GET | `/api/user/logs` | 我的调用记录（分页） |
| POST | `/api/user/reset-token` | 重置我的令牌 |
| PUT | `/api/user/profile` | 修改备注等资料 |
| GET | `/user` | 用户中心页面 |

账号登录使用服务端会话 cookie `nfentik_session`，写操作需在请求头携带 `X-CSRF-Token`（值与 `nfentik_csrf` cookie 一致）。`/query` 仍只使用 token 鉴权；同时携带 Bearer token 时以 token 身份为准。

## 管理接口

所有管理接口位于 `/api/admin/` 下，需在请求头携带管理员令牌。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / PUT | `/api/admin/settings` | 读取/更新系统设置（AppSettings） |
| — | `/api/admin/questions` | 题目增删改查、移动、复制、待修正标记 |
| GET / DELETE | `/api/admin/logs`、`/api/admin/logs/clear` | 请求日志查询与清空 |
| GET / PUT | `/api/admin/model-config` | 模型配置读写 |
| GET / PUT | `/api/admin/external-banks` | 第三方题库配置读写 |
| POST | `/api/admin/external-banks/test` | 第三方题库连通性测试 |
| — | `/api/admin/mogo` | MOGO 多源题库导入（源管理、计划、限速、运行进度） |
| GET | `/api/admin/stats` | 概览统计（含数据库结构版本） |
| — | `/api/admin/users` | 用户增删改查、续费、重置令牌 |
| — | `/api/admin/accounts` | 账号列表、重置密码、启停、解绑/绑定、撤销会话 |
| — | `/api/admin/redeem` | 卡密批次创建、列表与作废 |
| GET / POST | `/api/admin/plans` | 套餐列表、新增套餐 |
| PUT / DELETE | `/api/admin/plans/{code}` | 修改、停用/删除套餐 |
| GET | `/console` | 管理控制台页面 |

### 套餐设置

套餐目录保存在 `Plans` 表，默认播种包月、包季、半年、包年、无限、自定义时长、按次 7 个套餐。套餐代码和类型创建后不可修改。

`GET /api/admin/plans` 返回：

```json
{
  "success": true,
  "plans": [
    {
      "Code": "count_1000",
      "Label": "1000 次",
      "Kind": "count",
      "Days": 0,
      "QuotaCount": 1000,
      "Unlimited": false,
      "Custom": false,
      "ExpiryDays": 0,
      "Enabled": true,
      "Sort": 80,
      "UserCount": 12,
      "BatchCount": 3
    }
  ]
}
```

新增/修改请求字段使用 snake_case：`code`、`label`、`kind`、`days`、`quota_count`、`unlimited`、`custom`、`expiry_days`、`enabled`、`sort`、`price_cents`、`currency`、`description`、`badge`。

规则：

- `duration` 套餐使用 `days`、`unlimited`、`custom`。
- `count` 套餐使用 `quota_count`、`custom`、`expiry_days`。
- 停用套餐不再出现在新建用户、续费和新卡密下拉框中，已发出的未使用卡密仍可兑换。
- 有用户权益或卡密批次引用时不能删除，只能停用。
- 默认套餐必须保持启用；至少保留一个启用套餐。

### 用户双权益

用户可同时持有 `duration` 和 `count` 两条权益，`GET /api/user/me` 的 `user.entitlements` 返回：

```json
[
  {
    "kind": "duration",
    "plan_code": "monthly",
    "plan_label": "包月",
    "start_at": "2026-10-27 10:00:00",
    "expire_at": "2026-11-26 10:00:00",
    "unlimited": false,
    "status": "active"
  },
  {
    "kind": "count",
    "plan_code": "count_1000",
    "plan_label": "1000 次",
    "total_count": 1000,
    "used_count": 200,
    "remain_count": 800,
    "status": "active"
  }
]
```

查询时优先使用有效时长权益，此时次数余额不扣减；时长到期后自动扣减次数。时长套餐可叠加到期时间，次数套餐可叠加次数。当前为无限时长时，兑换时长卡会被拒绝。

### 第三方题库配置

`/api/admin/external-banks` 读写 `AppSettings` 键 `external_banks`：

```json
{
  "sources": [
    {
      "id": "src-1",
      "name": "示例题库",
      "enabled": true,
      "priority": 10,
      "url": "https://api.example.com/search?q={question}",
      "method": "GET",
      "headers": { "Authorization": "Bearer <API_KEY>" },
      "queryParams": { "q": "{question}" },
      "bodyTemplate": "",
      "contentType": "application/json",
      "answerPath": "data.answer",
      "questionPath": "data.question",
      "timeoutSeconds": 10,
      "trust": false,
      "writeBack": true,
      "cacheTTL": 0
    }
  ]
}
```

保存时校验：`name` 与 `url` 非空、`url` 为合法 http/https。占位符 `{question}`、`{options}`、`{type}` 在地址、参数与请求体中替换；`answerPath` 优先，留空或未取到答案时自动识别响应中的答案字段（支持嵌套对象、数组、数字与常见键名，并排除状态字段），`questionPath` 留空时自动识别回显题目用于校验。

测试接口 `POST /api/admin/external-banks/test`：

```json
{ "source": { "url": "https://api.example.com/q?title={question}", "method": "GET", "answerPath": "data.answer" }, "question": "1+1=" }
```

成功返回 `{"success": true, "answer": "...", "question": "..."}`，失败返回 `{"success": false, "message": "..."}`。日志中的 `Authorization`、`X-Api-Key` 等敏感请求头在写入前会脱敏。

### MOGO 题库导入

从自有的 MOGO 控制台题库导入题目到本地题库，支持配置多个源、按计划定时增量导入，并对导入限速。接口位于 `/api/admin/mogo` 下：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/admin/mogo/sources` | 列出已配置的源 |
| PUT | `/api/admin/mogo/sources` | 覆盖保存源列表 |
| POST | `/api/admin/mogo/sources/{id}/run` | 立即运行某个源 |
| POST | `/api/admin/mogo/stop` | 停止当前导入任务 |
| GET | `/api/admin/mogo/status` | 当前任务进度 |
| POST | `/api/admin/mogo/test` | 测试某个 HTTP 源的连通性 |
| POST | `/api/admin/mogo/db-test` | 测试数据库源并返回可映射的列名 |

源配置字段（`sources` 数组元素）：

```json
{
  "id": "mogo-1",
  "name": "MOGO 主库",
  "type": "http",
  "enabled": true,
  "overwrite": false,
  "baseUrl": "http://<mogo-host>:9988",
  "aKey": "<aKey>",
  "item": 50,
  "mode": "incremental",
  "schedule": { "type": "manual", "everyMinutes": 60, "atTime": "03:00" },
  "throttle": { "pageDelayMs": 1500, "rowDelayMs": 0, "maxPerRun": 0 },
  "maxId": 0,
  "lastRun": "",
  "lastImported": 0,
  "lastUpdated": 0
}
```

- `type`：`http`（默认）或 `database`。数据库源使用 `database` 对象（`host/port/user/password/database/table/batchSize/columns`），并用 `throttle.rowsPerSecond` 控制每秒读取行数、`batchDelayMs` 控制批间延迟。
- `mode`：`incremental`（仅导入比游标更新的数据，首次等价回填）或 `backfill`。
- `schedule.type`：`manual`（仅手动）/ `interval`（每 `everyMinutes` 分钟）/ `daily`（每天 `atTime` 时刻）。调度由服务端每 30 秒检查一次，同一时刻只运行一个任务。
- `throttle`：`pageDelayMs` 每页间隔、`rowDelayMs` 每行间隔、`rowsPerSecond` 数据库源每秒行数、`batchDelayMs` 数据库源批间延迟、`maxPerRun` 单次上限（0 不限）。
- `overwrite`：默认 `false`，命中同题同选项时跳过并保留已有答案；设为 `true` 时用导入答案覆盖已有答案，来源改为 `import:mogo-db`、清除 AI 与待修正标记，题目与选项文本保持不变。
- HTTP 源按 id 倒序拉取并去重写入，来源标记 `import:mogo`；数据库源按主键游标升序读取，来源标记 `import:mogo-db`。`maxId`/`lastRun`/`lastImported`/`lastUpdated` 为持久化的运行游标与计数。
- `GET /api/admin/mogo/status` 的 `state` 包含 `imported/updated/skipped/scanned/currentId/total` 等进度字段。

## 客户端集成契约

- OCS 用户脚本：把查询地址指向 `http://<服务器>:<端口>/query`，并把令牌填入配置。
- 第三方题库：只需返回 JSON，答案会自动识别；如需精确指定字段，可填 `answerPath`。
- AI 模型：兼容 OpenAI 的 `/v1/chat/completions`（含流式）接口。
