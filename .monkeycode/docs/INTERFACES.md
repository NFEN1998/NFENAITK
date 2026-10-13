# nfentik-go 接口文档

nfentik-go 对外提供三类 HTTP 接口：面向 OCS 用户脚本的查询接口、面向普通用户的用户接口、面向管理员的管理接口。认证通过请求头 `Authorization: Bearer <token>` 或查询参数/请求体携带令牌。

## 认证

| 接口 | 凭据 | 说明 |
| --- | --- | --- |
| `/query`、`/api/model/*` | 可选 `usertoken` | 是否强制由设置 `requireTokenForQuery` 决定；`usersEnabled` 关闭时不校验 |
| `/api/user/*` | `usertoken` | 普通用户令牌，前缀通常为 `nf_` |
| `/api/admin/*` | `adminToken` | 管理员令牌；当 `adminToken` 为空时管理接口不校验 |

## OCS 查询接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / HEAD | `/` | 根路径，`HEAD` 返回 `Hello,OCS` 并广播事件 |
| GET / POST | `/query` | 题库查询（核心接口） |
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

GET 形式接受查询参数 `title`（别名 `q`）、`options`、`type`、`raw`、`token`。

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

## 用户接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/user/login` | 用户令牌登录 |
| GET | `/api/user/me` | 当前用户与套餐信息 |
| GET | `/api/user/logs` | 我的调用记录（分页） |
| POST | `/api/user/reset-token` | 重置我的令牌 |
| PUT | `/api/user/profile` | 修改备注等资料 |
| GET | `/user` | 用户中心页面 |

## 管理接口

所有管理接口位于 `/api/admin/` 下，需在请求头携带管理员令牌。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / PUT | `/api/admin/settings` | 读取/更新系统设置（AppSettings） |
| — | `/api/admin/questions` | 题目增删改查、移动、复制、待修正标记 |
| — | `/api/admin/folders` | 文件夹增删改查与移动 |
| GET / DELETE | `/api/admin/logs`、`/api/admin/logs/clear` | 请求日志查询与清空 |
| GET / PUT | `/api/admin/model-config` | 模型配置读写 |
| GET / PUT | `/api/admin/external-banks` | 第三方题库配置读写 |
| POST | `/api/admin/external-banks/test` | 第三方题库连通性测试 |
| — | `/api/admin/mogo` | MOGO 多源题库导入（源管理、计划、限速、运行进度） |
| GET | `/api/admin/stats` | 概览统计（含数据库结构版本） |
| — | `/api/admin/users` | 用户增删改查、续费、重置令牌 |
| GET | `/console` | 管理控制台页面 |

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
| POST | `/api/admin/mogo/test` | 测试某个源连通性 |

源配置字段（`sources` 数组元素）：

```json
{
  "id": "mogo-1",
  "name": "MOGO 主库",
  "enabled": true,
  "baseUrl": "http://<mogo-host>:9988",
  "aKey": "<aKey>",
  "folderId": 0,
  "item": 50,
  "mode": "incremental",
  "schedule": { "type": "manual", "everyMinutes": 60, "atTime": "03:00" },
  "throttle": { "pageDelayMs": 1500, "rowDelayMs": 0, "maxPerRun": 0 },
  "maxId": 0,
  "lastRun": "",
  "lastImported": 0
}
```

- `mode`：`incremental`（仅导入比游标更新的数据，首次等价回填）或 `backfill`。
- `schedule.type`：`manual`（仅手动）/ `interval`（每 `everyMinutes` 分钟）/ `daily`（每天 `atTime` 时刻）。调度由服务端每 30 秒检查一次，同一时刻只运行一个任务。
- `throttle`：`pageDelayMs` 每页间隔、`rowDelayMs` 每行间隔、`maxPerRun` 单次上限（0 不限），用于控制对 MOGO 的压力。
- 导入采用 MOGO 递增 id 作游标、按 id 倒序拉取并去重写入，来源标记 `import:mogo`；`maxId`/`lastRun`/`lastImported` 为持久化的运行游标。

## 客户端集成契约

- OCS 用户脚本：把查询地址指向 `http://<服务器>:<端口>/query`，并把令牌填入配置。
- 第三方题库：只需返回 JSON，答案会自动识别；如需精确指定字段，可填 `answerPath`。
- AI 模型：兼容 OpenAI 的 `/v1/chat/completions`（含流式）接口。
