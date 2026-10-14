# 需求实施计划

- [x] 1. 新增 `internal/mogodb` 只读读取包
  - [x] 1.1 定义 `Config`、`ColumnMap`、`Row` 类型与 DSN 组装（Requirement 1.2、2.7；Design「新增包 internal/mogodb」）
    - 默认列映射 `id/question/answer/opt/questionType`
    - DSN：`user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=false&timeout=5s&readTimeout=30s`，密码转义
  - [x] 1.2 实现标识符白名单校验与 `Open`/`Columns`/`Count`/`Fetch`/`Close`（Requirement 2.1、2.2、2.3、2.4、5.5；Design 正确性属性 1、7）
    - 表名/列名匹配 `^[A-Za-z_][A-Za-z0-9_]*$` 后反引号包裹
    - `Fetch` 升序：`SELECT ... WHERE pk > ? ORDER BY pk ASC LIMIT ?`，仅 SELECT
    - `Columns` 读 `information_schema.columns` 供连接测试
  - [x] 1.3 实现 `Limiter.Wait`（每秒行数 + 批间延迟，响应 ctx 取消）（Requirement 3.3、3.4；Design 正确性属性 5）
  - [x] 1.4 为 mogodb 编写单元测试（DSN、标识符校验、SQL 组装、Limiter 速率与取消）
  - [x] 1.5 性质测试：`rowsPerSecond > 0` 时平均读取速率不超过设定值（Design 正确性属性 5）

- [x] 2. 服务端数据模型与源归一化
  - [x] 2.1 `mogoSource` 增加 `Type` 与 `Database` 字段，`mogoThrottle` 增加 `RowsPerSecond`（Requirement 1.1、1.2；Design「服务端集成」）
  - [x] 2.2 `normalizeSource` 处理类型默认（`http`）、批次收敛 1–5000（`<=0` 置 500）、`rowsPerSecond` 负值置 0（Requirement 1.5、3.2、3.6）
  - [x] 2.3 密码脱敏：返回时替换为占位符，保存时遇占位符保留原密码（Requirement 1.7）
  - [x] 2.4 为 `normalizeSource` 与密码脱敏编写单元测试

- [x] 3. 数据库导入运行链路
  - [x] 3.1 `runMogoSource` 按 `src.Type` 分派到数据库分支（Requirement 4.1、4.2）
    - `Server` 注入 `newMogoDBReader func(mogodb.Config) (mogoDBReader, error)`，默认 `mogodb.Open`
  - [x] 3.2 实现 `runMogoDatabaseSource`：连接、`Count` 填 `Total`、循环 `Fetch`、`mogo.SplitQuestion`+`mogo.ParseOptions`→`store.ImportQuestion`（来源 `import:mogo-db`）、累加计数、限速、每 N 批持久化游标（Requirement 2.1–2.8、3.1–3.7、4.3–4.6）
    - 复用 `finish`：更新 `MaxID/LastRun/LastImported/LastError` 并失效查询缓存
  - [x] 3.3 实现可重试驱动错误的退避重试（复用 `retries` 与固定 5s）（Requirement 5.1、5.2）
  - [x] 3.4 用假 reader 测试运行链路：增量仅读 `id > cursor`、计数正确、`MaxPerRun` 停止并留游标、连接失败/缺列错误、来源标记 `import:mogo-db`
  - [x] 3.5 性质测试：游标单调不减、中断后不重复导入（Design 正确性属性 2）

- [x] 4. 连接测试端点
  - [x] 4.1 新增 `POST /api/admin/mogo/db-test`：组装配置、`Open` + `Columns`，返回列名或错误；注册路由（Requirement 1.8）
  - [x] 4.2 端点的单元测试（成功返回列名、连接失败返回错误）

- [x] 5. 控制台 UI
  - [x] 5.1 通用弹窗支持条件字段 `showWhen(values)`；源编辑器增加「类型」下拉与数据库字段（主机/端口/用户名/密码/库名/表名/列映射/批次）（Requirement 1.1、1.2、1.6；Design「控制台」）
  - [x] 5.2 增加「每秒行数」「批间延迟毫秒」，新建数据库源默认批次 500、每秒 100、延迟 0（Requirement 3.2、3.4；已确认决策）
  - [x] 5.3 源列表显示「类型」列；数据库源「测试」按钮调用 `/mogo/db-test`（Requirement 4.7）

- [x] 6. 检查点 - 确保所有测试通过
  - 运行 `gofmt -l internal`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`
  - 如有疑问请询问用户
