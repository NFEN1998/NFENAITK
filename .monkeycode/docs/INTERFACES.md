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

保存时校验：`name` 与 `url` 非空、`url` 为合法 http/https。占位符 `{question}`、`{options}`、`{type}` 在地址、参数与请求体中替换；`answerPath` 为空时按 `data.answer` → `answer` → `data` 回退提取。

测试接口 `POST /api/admin/external-banks/test`：

```json
{ "source": { "url": "https://api.example.com/q?title={question}", "method": "GET", "answerPath": "data.answer" }, "question": "1+1=" }
```

成功返回 `{"success": true, "answer": "...", "question": "..."}`，失败返回 `{"success": false, "message": "..."}`。日志中的 `Authorization`、`X-Api-Key` 等敏感请求头在写入前会脱敏。

## 客户端集成契约

- OCS 用户脚本：把查询地址指向 `http://<服务器>:<端口>/query`，并把令牌填入配置。
- 第三方题库：只需返回 JSON，通过 `answerPath` 指定答案字段即可接入。
- AI 模型：兼容 OpenAI 的 `/v1/chat/completions`（含流式）接口。
