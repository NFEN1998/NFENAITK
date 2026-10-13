# store

数据存储层：负责题库、文件夹、请求日志、用户、系统设置与模型/第三方题库配置的持久化，并在 PostgreSQL 与 MySQL 之间通过方言层统一 SQL。

## 结构

```
internal/store/
├── store.go         # Store 结构、连接与查询/文件夹/题目 CRUD
├── dialect.go       # 数据库方言识别、占位符重绑、方言 SQL 片段
├── migrate.go       # 迁移系统、结构检查与修复、MySQL 建表
├── users.go         # 用户、套餐、使用与审计日志、使用报表
├── logs.go          # 请求日志、每日计数
└── settings_repo.go # AppSettings 键值读写
```

## 关键文件

| 文件 | 目的 |
| --- | --- |
| `store.go` | `Open` 连接与迁移；`Query` 两段式匹配；题目/文件夹 CRUD |
| `dialect.go` | 引擎差异集中在此，其余代码只写 `$n` 占位符 |
| `migrate.go` | 带版本号的迁移、完整性检查与幂等修复 |
| `users.go` | 用户令牌、套餐配额与报表 |
| `settings_repo.go` | `app_settings` / `model_config` / `external_banks` 读写 |

## 依赖

**本模块依赖**
- `internal/match` - 题目归一化、哈希与相似度

**依赖本模块的**
- `internal/server` - 所有业务读写
- `internal/config` - 设置持久化
- `internal/importer` - 旧数据导入

## 规范

### 代码模式

**查询与写入统一走方言辅助**：

```go
rows, err := s.query(`SELECT ... WHERE QuestionHash = $1 LIMIT $2`, hash, limit)
_, err = s.exec(`UPDATE ... SET Flag = $1 WHERE Id = $2`, true, id)
```

**需要返回自增 id 的插入**：

```go
id, err := s.insertReturningID(`INSERT INTO Folders (...) VALUES (...)`, args...)
```

### 错误处理
- 未找到返回 `ErrNotFound` / `ErrUserNotFound`；唯一键冲突经 `isUniqueViolation` 归一为 `ErrUserExists`

### 测试
- `testStore(t)` 连接 `TEST_DATABASE_URL`，无库时跳过
- 覆盖：`insert_ai_test.go`、`users_test.go`、`integrity_test.go`
