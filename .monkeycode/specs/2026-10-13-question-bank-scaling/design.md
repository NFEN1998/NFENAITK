# 题库大数据量改造方案（搜索下推 + 游标分页）

Feature Name: question-bank-scaling
Updated: 2026-10-13
状态: 待评审

## 背景与问题

面向 5,000 万题规模（当前已切到 PostgreSQL），现有控制台题库读取存在两处与数据量强相关的瓶颈：

1. **搜索是全表读入内存后在 Go 侧过滤**。`internal/store/store.go` 的 `Search` 执行
   `SELECT <cols> FROM AIResponses` 取回全部行，再在 Go 里 `strings.Contains` 过滤。
   5,000 万行下会耗尽内存与耗时；且无法分页。
2. **分页用 `LIMIT/OFFSET`**。`PaginatedQuestions` 用 `ORDER BY CreateTime DESC LIMIT n OFFSET m`
   并额外 `SELECT COUNT(*)`。深翻页时 `OFFSET` 越大越慢（数据库需跳过 m 行），
   `COUNT(*)` 在 5,000 万行/带过滤时同样昂贵。

其他一次性全量读取点：`PendingCorrectionQuestions`（取全部待修正）、`RecursiveQuestions`（已随文件夹移除而删除）。

## 目标与非目标

**目标**
- 题库「搜索」下推到 SQL，按关键字分页返回，5,000 万行下可用。
- 题库列表翻页改为游标（keyset）分页，翻到任意深度耗时稳定。
- 保持现有功能与交互可用（搜索、排序、待修正筛选、增删改）。

**非目标**
- 不引入新的外部服务（如 Elasticsearch）。
- 不改变题库数据结构与存量数据。
- MySQL 上不做等同优化（见「MySQL 兼容」），以 PostgreSQL 为主。

## 现状（代码定位）

- `internal/store/store.go`
  - `Search(keyword)`：全表读入 + 内存过滤（瓶颈 1）。
  - `PaginatedQuestions(pendingOnly, page, pageSize, sortOrder)`：`LIMIT/OFFSET` + `COUNT(*)`（瓶颈 2）。
  - `questionColumns`：`ar.Id, ar.Question, ar.Options, ar.Answer, ar.QuestionType, ar.CreateTime, ar.IsAi, COALESCE(ar.IsPendingCorrection, FALSE)`。
- `internal/server/admin.go`：`/api/admin/questions`（列表）、`/api/admin/questions/search`（搜索）。
- `web/static/console.js`：`loadQuestions` 组装 `page/page_size/sort` 或 `keyword`；分页器为 `#q-prev/#q-next + #q-pageinfo`。
- 已有索引：`QuestionHash`、`CreateTime`、`IsPendingCorrection`、`Question` 上的 pg_trgm GIN（`idx_airesponses_question_trgm`）。

## 方案总览

- 搜索：新增 pg_trgm GIN 索引（Question/Options/Answer），把关键字转为若干 `ILIKE '%term%'` 的 AND 条件下推到 SQL；`PageSize` 受限，返回分页。
- 分页：改为按 `(CreateTime, Id)` 的 keyset 游标；新增复合索引；去掉精确 `COUNT(*)`（改为「还有更多」判断）。
- 接口与前端：分页器改用游标（上一页用游标栈，下一页用 `next_cursor`）。

---

## 1. 搜索下推（PostgreSQL）

### SQL 形态

对关键字按空白拆分为多个 term，逐个 AND，命中任一字段即可（这里取「每个 term 需出现在合并文本中」的语义，与原 Go 版一致：原版是把 question+options+answer 拼接后要求包含每个 term）：

```sql
-- 每个 term 生成一组 (Question ILIKE $k OR Options ILIKE $k OR Answer ILIKE $k)
SELECT ar.Id, ar.Question, ar.Options, ar.Answer, ar.QuestionType,
       ar.CreateTime, ar.IsAi, COALESCE(ar.IsPendingCorrection, FALSE)
FROM AIResponses ar
WHERE (ar.Question ILIKE $1 OR ar.Options ILIKE $1 OR ar.Answer ILIKE $1)
  AND (ar.Question ILIKE $2 OR ar.Options ILIKE $2 OR ar.Answer ILIKE $2)
ORDER BY ar.CreateTime DESC, ar.Id DESC
LIMIT $N;
```

- `pg_trgm` 的 `gin_trgm_ops` 支持 `ILIKE '%kw%'`，可走 GIN 索引；多 term 用 AND 组合时，PG 可用索引位图求交。
- term 上下限（例如最多 5 个）与最小长度（≥2）做约束，避免退化。
- 关键字为空的搜索等同于普通列表（走第 2 节的游标分页）。

### 索引

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_question_trgm
  ON AIResponses USING gin (Question gin_trgm_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_options_trgm
  ON AIResponses USING gin (Options gin_trgm_ops);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_answer_trgm
  ON AIResponses USING gin (Answer gin_trgm_ops);
```

（`Question` 的 trgm 索引已存在，复用。）

### 接口

```
GET /api/admin/questions/search?keyword=<kw>&cursor=<token>&page_size=20
→ { success, items:[...], next_cursor, has_more }
```

### 备选（若 trgm 效果不足）

- 增加冗余列 `SearchText`（question+options+answer 归一化小写），仅建一个 GIN trgm 索引，`WHERE SearchText ILIKE '%kw%'`，条件更简单、索引更少；代价是写入时维护该列。可作为第二期。

---

## 2. 游标（keyset）分页

### 语义

- 默认排序 `CreateTime DESC, Id DESC`；`sort=asc` 时反向。
- 游标为不透明串：`base64url(json{"t":"<createTime>","i":<id>})`，包含上一页最后一行的排序键。
- 下一页：

```sql
SELECT <cols> FROM AIResponses ar
WHERE (ar.CreateTime, ar.Id) < ($t, $i)     -- DESC；ASC 时用 >
ORDER BY ar.CreateTime DESC, ar.Id DESC
LIMIT $limit;
```

- 「上一页」：用 UI 维护的游标栈（进入下一页前把当前游标入栈），或在 SQL 侧反向查询后逆序（本方案采用游标栈，简单且稳定）。
- 「还有更多」：取 `limit+1` 条，多出的 1 条用于判断 `has_more`，不返回。
- 去掉 `SELECT COUNT(*)`：大数据量下精确总数代价高。改为不返回 total；前端显示「已加载 N 条 · 还有更多/到底了」。若仍需总量，可后续用 `pg_class.reltuples` 之类估算（可选，标注为近似）。

### 待修正筛选

```sql
WHERE COALESCE(ar.IsPendingCorrection, FALSE) = TRUE
  AND (ar.CreateTime, ar.Id) < ($t, $i)
ORDER BY ar.CreateTime DESC, ar.Id DESC LIMIT $limit;
```

### 索引

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_created_id
  ON AIResponses (CreateTime DESC, Id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_airesponses_pending_created_id
  ON AIResponses (CreateTime DESC, Id DESC)
  WHERE COALESCE(IsPendingCorrection, FALSE);
```

（`IsPendingCorrection` 当前是普通列；部分索引的谓词需与查询条件完全一致，故查询里也写 `COALESCE(...)=TRUE` 或统一改成直接 `IsPendingCorrection`，实施时二选一保持一致。）

### 接口

```
GET /api/admin/questions?cursor=<token>&page_size=20&pending=true&sort=desc
→ { success, items:[...], next_cursor, has_more }
```

`api/admin/questions/{id}`、`pending-correction` 等单条接口不变。

---

## 3. 其他一次性读取点

- `PendingCorrectionQuestions()`：改为分页（复用第 2 节 keyset），或仅用于导出时限制上限。
- `PendingCorrectionCount()`：`COUNT(*)` 带过滤，5,000 万行下较慢；概览处可异步/缓存，或标注为近似。
- `DailyRequestCounts`、`FolderStats`（已随文件夹删除）等不涉及题库大表，维持不变。

---

## 4. 迁移（DDL）

新增迁移 v7（PostgreSQL）：以 `CREATE INDEX CONCURRENTLY` 建立上述 4 个索引（trgm×2 新增 + created_id + pending_created_id）。MySQL 对应普通索引（无 trgm）。

注意：`CONCURRENTLY` 不能放在事务里，需在迁移执行器中单独串行执行（现有 `ensureTrigram`/`ensureQuestionHashIndex` 已是此模式，可复用）。

---

## 5. 后端改动清单

- `internal/store/store.go`
  - 新增 `SearchQuestions(keyword string, cursor string, limit int) (Page, error)`（下推 SQL + 游标）。
  - 改 `PaginatedQuestions(pendingOnly bool, cursor string, limit int, sortOrder string) (Page, error)`（keyset + has_more）；`Page` 增加 `NextCursor string`、`HasMore bool`，移除 `Total`（或保留为可选近似）。
  - 删除旧的 `Search`。
- `internal/server/admin.go`
  - `/questions`、`/questions/search` 改为解析 `cursor/page_size`，返回 `next_cursor/has_more`。
- 索引与迁移：`internal/store/migrate.go` 增加 v7 与索引创建。

## 6. 前端改动清单（console.js / console.html）

- `loadQuestions`：不再传 `page`；维护 `state.cursor`、`state.cursorStack`；点击「下一页」用 `next_cursor`、入栈当前游标；「上一页」出栈。「共 N 条」改为「已加载 N 条」。
- 搜索：改为调用 `/questions/search?keyword=&cursor=`，复用同一分页器。
- 待修正页：改用游标分页（或限制加载条数 + 「加载更多」）。

## 7. 错误处理与边界

- 非法/过期游标：回退为第一页（忽略游标）。
- `page_size` 上限（如 100–200），关键字 term 数与长度上限。
- `CreateTime` 为空的行：DDL 有默认值，实际非空；游标比较用 `(CreateTime, Id)` 组合保证稳定排序。
- 并发写入：keyset 分页对新增/删除天然容忍（可能跳过或重复边界行），可接受。

## 8. 测试与压测

- 单测：游标翻页不重不漏（构造含相同 CreateTime 的多行，验证 Id 兜底排序）；搜索与旧实现结果一致（在同一数据集上比对）。
- 集成测：`/api/admin/questions` 与 `/search` 的分页链路（PG 下跑）。
- 压测：本地造 100 万→1,000 万行，验证第 1 页、深翻页（keyset 应恒定）、搜索在有/无命中下的耗时；`EXPLAIN (ANALYZE, BUFFERS)` 确认走索引、无 Seq Scan。

## 9. 灰度与回滚

- 索引用 `CONCURRENTLY` 建立，不锁表；新接口与前端一起发布（内部系统，无需兼容旧前端）。
- 回滚：保留旧 `PaginatedQuestions` 逻辑分支一个版本，或直接回退发布；索引可保留（无害）。

## 10. 工作量估计

- 后端（store + server + 迁移/索引）：约 1 天
- 前端（分页器改游标 + 搜索）：约 0.5 天
- 测试与压测：约 0.5 天
- 合计：约 2 人天

## MySQL 兼容

- keyset 分页在 MySQL 同样有效（走 `(CreateTime, Id)` 复合索引）。
- 搜索在 MySQL 无 pg_trgm：`LIKE '%kw%'` 无法用普通索引，需 InnoDB `ngram` FULLTEXT 或前缀索引 + 左锚定。当前部署以 PG 为主，MySQL 侧搜索暂按「可用但不快，后续再优化」处理，并在文档标注。

## 参考

- pg_trgm：`gin_trgm_ops` 支持 `LIKE/ILIKE` 加速。
- PostgreSQL keyset（seek）分页：以复合排序键做范围比较，避免 `OFFSET`。
