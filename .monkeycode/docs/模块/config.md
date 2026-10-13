# config

配置层：区分「本地引导配置」与「数据库配置」。引导配置只保存数据库与 Redis 连接串；其余设置（主题、令牌、网络、模型与第三方题库）存于数据库，换机器/目录不丢失。

## 结构

```
internal/config/
├── config.go  # 设置结构、BootstrapFiles、Store 与读写方法
└── legacy.go  # 旧版 config.json / model_config.json 导入
```

## 关键文件

| 文件 | 目的 |
| --- | --- |
| `config.go` | `Settings`、`DefaultSettings`、`LoadBootstrap` / `WriteBootstrap`、`Store` |
| `legacy.go` | `ImportLegacyFiles`：首次启动把旧文件导入数据库并删除 |

## 依赖

**本模块依赖**
- `internal/store`（通过 `SettingsRepo` 接口：`GetSetting` / `SetSetting` / `DeleteSetting`）

**依赖本模块的**
- `internal/server`、`main.go`

## 规范

### 代码模式

**读取/保存数据库中的配置文档**：

```go
raw, err := s.config.ExternalBanks()      // {"sources":[...]}
err = s.config.SaveExternalBanks(body)
```

### 配置来源优先级
- 环境变量 > 本地 `config/config.json`
- `DATABASE_URL` 覆盖引导配置中的数据库连接串；`REDIS_URL` 覆盖 Redis 连接串

### 数据位置
- 引导配置：可执行文件同级的 `config/config.json`
- 应用设置：数据库 `AppSettings` 表键 `app_settings`
- 模型配置：键 `model_config`；第三方题库：键 `external_banks`
- 换机器只要连同一数据库即保留全部设置
