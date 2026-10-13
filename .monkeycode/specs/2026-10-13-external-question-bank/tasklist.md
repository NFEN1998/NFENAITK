# 需求实施计划

- [ ] 1. 搭建 externalbank 包与配置模型
  - [ ] 1.1 定义 Source / Settings / Query / Result 类型与 Parse / Enabled
    - 在 `internal/externalbank` 定义配置结构与方法，覆盖 R1.2、R1.4、R2.1
    - `Enabled()` 过滤未启用与非法 URL 的源，并按 Priority 升序排序
  - [ ] 1.2 实现 config.Store.ExternalBanks / SaveExternalBanks
    - 以 AppSettings 新键 `external_banks` 持久化 JSON，与 `model_config` 对称（R1.2）
  - [ ] 1.3 为 Parse / Enabled 编写单元测试
    - 覆盖非法 URL 剔除、优先级排序、空文档容错（R1.4、R1.5）

- [ ] 2. 实现 HTTP 适配器 Client.Lookup
  - [ ] 2.1 实现 GET / POST 请求构造与占位符渲染
    - 地址、query 参数、请求体支持 `{question}` / `{options}` / `{type}`（R2.1、R2.2）
  - [ ] 2.2 实现响应解析与答案路径提取
    - 点号路径下钻，空路径按 `data.answer` → `answer` → `data` 回退（R2.3、R2.4、R2.5）
  - [ ] 2.3 实现每源超时与错误返回
    - 使用 `context.WithTimeout`，默认 10 秒；超时/非 2xx 返回错误（R6.1、R6.3）
  - [ ] 2.4 用 httptest 编写适配器单元测试
    - 覆盖 GET/POST、占位替换、路径提取、空/非法响应、超时

- [ ] 3. 数据层来源标记与第三方写入
  - [ ] 3.1 新增 migration v6：AIResponses 增加 Source 列
    - PostgreSQL 与 MySQL 双引擎 DDL，列默认空串（数据模型设计）
  - [ ] 3.2 新增 InsertExternalResponse 并标记 AI 来源
    - 复用去重逻辑；`InsertAIResponse` 写入 `Source='ai'`（R4.1、R4.2）
  - [ ] 3.3 为 InsertExternalResponse 编写单元测试
    - 覆盖同题去重与 Source 列写入

- [ ] 4. 查询链路接入第三方题库
  - [ ] 4.1 在 resolveQueryUncached 本地未命中后插入第三方链
    - 解析配置、按 `Enabled()` 顺序调用、全失败则进入 AI（R3.1、R3.3、R3.4）
  - [ ] 4.2 实现 acceptExternal 答案校验
    - 空答案过滤、`ai.IsUnreliableAnswer`、相似度阈值 0.72、Trust 绕过（R5.1—R5.5）
  - [ ] 4.3 实现命中回写与缓存
    - 回写复用 `FindByNormalizedQuestion` 去重；写缓存；URL 题目跳过缓存（R4.3—R4.5、R3.5）
  - [ ] 4.4 来源标记与回写容错
    - 日志与用户使用记录来源标为 `external:<name>`；回写失败仍返回答案（R6.4、R6.5）
  - [ ] 4.5 编写链路集成测试
    - httptest 源覆盖：命中回写、回落 AI、Trust 与校验分支、未配置源回归（R3、R5）

- [ ] 5. 控制台配置接口与视图
  - [ ] 5.1 新增 GET / PUT /api/admin/external-banks
    - 读写配置并校验（R1.1、R1.2、R1.7 脱敏）
  - [ ] 5.2 新增 POST /api/admin/external-banks/test
    - 复用 `Client.Lookup` 做连通性测试（R1.6）
  - [ ] 5.3 控制台新增「第三方题库」视图
    - 源列表增删改、启停、排序、测试连通性（R1.1、R1.2、R1.6）
  - [ ] 5.4 编写接口测试
    - 覆盖参数校验拒绝与连通性测试结果

- [ ] 6. 整体验证
  - [ ] 6.1 全量 build / vet 与双引擎回归
    - PostgreSQL 与 MySQL 运行 `go test -p 1 ./...`（R3.4）
  - [ ] 6.2 检查点 - 确保所有测试通过，如有疑问请询问用户
