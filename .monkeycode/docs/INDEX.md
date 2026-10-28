# nfentik-go 文档

本目录记录 nfentik-go（NFENTK 服务端）的架构、接口与开发指南，面向维护者与集成方。所有内容基于 `nfentik-go/` 实际代码与脚本。

**快速链接**: [架构](./ARCHITECTURE.md) | [接口](./INTERFACES.md) | [开发者指南](./DEVELOPER_GUIDE.md)

---

## 核心文档

### [架构](./ARCHITECTURE.md)
系统设计、技术栈、子系统划分与查询数据流。从这里了解服务如何运作。

### [接口](./INTERFACES.md)
HTTP 端点、`/query` 请求响应、第三方题库配置与认证方式。

### [开发者指南](./DEVELOPER_GUIDE.md)
环境搭建、构建运行、测试与打包、常见任务与编码规范。

---

## 模块

| 模块 | 描述 | README |
| --- | --- | --- |
| `internal/server/` | HTTP 服务、路由、控制台与用户中心 | [README](./模块/server.md) |
| `internal/store/` | PostgreSQL / MySQL 存储与题库查询 | [README](./模块/store.md) |
| `internal/externalbank/` | 第三方题库 HTTP 适配器 | [README](./模块/externalbank.md) |
| `internal/ai/` | AI 模型调用与结果解析 | [README](./模块/ai.md) |
| `internal/cache/` | Redis 查询缓存、计数与日志队列 | [README](./模块/cache.md) |
| `internal/match/` | 题目匹配算法（jieba 分词） | [README](./模块/match.md) |
| `internal/config/` | 引导配置与数据库配置读写 | [README](./模块/config.md) |

---

## 核心概念

| 概念 | 描述 |
| --- | --- |
| [第三方题库源](./专有概念/第三方题库源.md) | 可配置的外部题库接口及其适配 |
| [用户令牌与套餐](./专有概念/用户令牌与套餐.md) | 普通用户鉴权与配额体系 |
| [查询链路](./专有概念/查询链路.md) | 缓存→本地→第三方→AI 的答案解析顺序 |
| [数据库方言](./专有概念/数据库方言.md) | PostgreSQL 与 MySQL 的统一适配 |

---

## 入门指南

### 项目新人？
1. [架构](./ARCHITECTURE.md) - 了解全局
2. [核心概念](#核心概念) - 学习领域术语
3. [开发者指南](./DEVELOPER_GUIDE.md) - 搭建环境

### 需要集成？
1. [接口](./INTERFACES.md) - 端点与契约
2. [架构](./ARCHITECTURE.md) - 系统边界与数据流

### 首次贡献？
1. [开发者指南](./DEVELOPER_GUIDE.md) - 搭建与工作流
2. [常见任务](./DEVELOPER_GUIDE.md#常见任务) - 分步指南

---

## 快速参考

### 命令

```bash
go build -o nfentik-go .            # 构建
go test -p 1 ./...                  # 串行运行测试
./nfentik-go -port 3000 -lan        # 启动服务
scripts/packaging/build.sh          # Linux 打包
scripts/packaging/build-windows.sh  # Windows 打包
```

### 重要文件

| 文件 | 目的 |
| --- | --- |
| `main.go` | 入口与子命令 |
| `internal/server/query.go` | 查询链路 |
| `internal/store/migrate.go` | 数据库迁移 |
| `internal/externalbank/client.go` | 第三方题库适配核心 |
| `config/config.json` | 本地引导配置（连接串） |
