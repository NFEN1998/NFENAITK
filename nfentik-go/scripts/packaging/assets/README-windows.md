# nfentik-go Windows 安装包

本包为 nfentik-go 预编译发布版，适用于 Windows 10/11 amd64，无需 Go 环境即可运行。

## 包内容

```
nfentik-go.exe          # 预编译二进制（windows/amd64）
config/
  config.example.json   # 引导配置示例（数据库 / Redis 连接串）
dict/                   # gojieba 中文分词词典（程序启动时自动加载）
start.bat               # 启动脚本
README.md               # 本文件
```

## 运行时依赖

| 组件 | 是否必需 | 最低版本 | 用途 |
| --- | --- | --- | --- |
| PostgreSQL | 二选一 | 13+ | 题库、用户、日志、配置的持久化存储 |
| MySQL / MariaDB | 二选一 | MySQL 5.7+ / MariaDB 10.2+ | 同上，连接串用 `mysql://` |
| Redis | 可选 | 5+ | 查询缓存与日志队列；缺失时自动退化为直连数据库 |

数据库引擎由连接串前缀自动识别：`postgres://` 或 `mysql://`。

软件本身已静态链接 C/C++ 运行库，无需额外安装 VC++ 运行库或 MinGW 运行库。

安装 PostgreSQL（任选其一）：

- 官方安装包：https://www.postgresql.org/download/windows/
- Chocolatey：`choco install postgresql`
- Scoop：`scoop install postgresql`

安装 Redis（可选）：

- Memurai（Windows 原生兼容 Redis）：https://www.memurai.com/
- WSL 内运行 Redis，再让 nfentik-go 通过 `redis://127.0.0.1:6379/0` 连接

## 创建数据库

用 `psql` 或 pgAdmin 创建数据库用户与库：

```sql
CREATE USER nfentik WITH PASSWORD 'nfentik';
CREATE DATABASE nfentik OWNER nfentik;
```

使用 MySQL / MariaDB 时，用客户端执行：

```sql
CREATE DATABASE nfentik CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
CREATE USER 'nfentik'@'%' IDENTIFIED BY 'nfentik';
GRANT ALL PRIVILEGES ON nfentik.* TO 'nfentik'@'%';
FLUSH PRIVILEGES;
```

对应连接串为 `mysql://nfentik:nfentik@127.0.0.1:3306/nfentik`。

服务首次启动会自动建表。

## 配置

程序目录下的 `config\config.json` 只保存数据库与 Redis 连接串：

```json
{
  "database": {
    "url": "postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable"
  },
  "redis": {
    "url": "redis://127.0.0.1:6379/0",
    "enabled": true,
    "prefix": "nfentik",
    "queryCacheTTL": 300,
    "historyEnabled": false
  }
}
```

也可以改用环境变量提供，环境变量优先级更高：

```bat
set DATABASE_URL=postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable
set REDIS_URL=redis://127.0.0.1:6379/0
```

## 运行

解压后，复制 `config\config.example.json` 为 `config\config.json` 并按需修改连接串，然后双击 `start.bat`，或在 `cmd` / PowerShell 中运行：

```bat
nfentik-go.exe -port 3000 -lan
```

启动后：

- 服务地址：`http://127.0.0.1:3000`
- 管理控制台：`http://127.0.0.1:3000/console`
- OCS 查询地址：`http://127.0.0.1:3000/query`

> `dict` 目录必须与 `nfentik-go.exe` 保持同级，程序启动时会自动加载其中的中文分词词典。如需把词典放到别处，可用环境变量 `JIEBA_DICT_DIR` 指向词典目录。

## 首次启动：安装向导

首次运行（未配置数据库或管理员令牌为空）会进入安装向导模式，浏览器访问：

```
http://127.0.0.1:3000/setup
```

按向导填写数据库连接、管理员令牌、站点名称。保存后**手动重启**程序生效。

## 参数说明

| 参数 | 说明 |
| --- | --- |
| `-port` | HTTP 端口，覆盖数据库中的设置 |
| `-bind` | 监听地址，覆盖数据库中的设置 |
| `-lan` | 监听 `0.0.0.0`，允许局域网访问；省略时仅监听 `127.0.0.1` |

## 运维命令

| 命令 | 用途 |
| --- | --- |
| `nfentik-go.exe` | 正常启动服务 |
| `nfentik-go.exe check-db` | 只读数据库完整性检测，结构不完整时退出非零 |
| `nfentik-go.exe migrate-sqlite -src <path>` | 从旧版 SQLite 数据库导入 |

连接串优先级：**环境变量 > 本地 `config\config.json`**。

## 注册为 Windows 服务

推荐用 WinSW 或 nssm 把程序托管为后台服务，实现开机自启。

### 方式一：WinSW

1. 下载 WinSW（https://github.com/winsw/winsw/releases），把 `WinSW-x64.exe` 放到程序目录并重命名为 `nfentik-go-service.exe`。
2. 在同一目录新建 `nfentik-go-service.xml`：

```xml
<service>
  <id>nfentik-go</id>
  <name>nfentik-go</name>
  <description>nfentik-go question bank service</description>
  <executable>%BASE%\nfentik-go.exe</executable>
  <arguments>-port 3000 -lan</arguments>
  <workingdirectory>%BASE%</workingdirectory>
  <env name="DATABASE_URL" value="postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable"/>
  <env name="REDIS_URL" value="redis://127.0.0.1:6379/0"/>
  <onfailure action="restart" delay="3 sec"/>
  <log mode="roll"/>
</service>
```

3. 以管理员身份运行：

```bat
nfentik-go-service.exe install
nfentik-go-service.exe start
```

### 方式二：nssm

```bat
nssm install nfentik-go "C:\path\to\nfentik-go.exe" -port 3000 -lan
nssm set nfentik-go AppDirectory "C:\path\to"
nssm set nfentik-go AppEnvironmentExtra DATABASE_URL=postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable
nssm start nfentik-go
```

## 验证安装

在 PowerShell 中执行：

```powershell
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:3000/ | Select-Object StatusCode
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:3000/console | Select-Object StatusCode
```

两者应返回 `200`。用管理员令牌登录控制台 `http://127.0.0.1:3000/console`。

## 从源码构建

需要 Go 1.24+ 与 CGO 工具链（中文分词 gojieba 和迁移功能 go-sqlite3 都依赖 CGO）。

- 在 Windows 上原生构建：安装 MSYS2 的 MinGW-w64 工具链，在 MSYS2 终端里执行 `scripts/packaging/build-windows.sh`。
- 在 Linux 上交叉编译：安装 `gcc-mingw-w64-x86-64` 与 `g++-mingw-w64-x86-64` 后执行 `scripts/packaging/build-windows.sh`。

## 升级

升级前务必备份数据库：

```bat
pg_dump "postgres://nfentik:nfentik@127.0.0.1:5432/nfentik?sslmode=disable" > backup.sql
```

替换 `nfentik-go.exe` 后重启程序（或重启服务），启动时会自动执行幂等迁移与结构修复，不会删除已有数据。

## 卸载

```bat
nfentik-go-service.exe stop
nfentik-go-service.exe uninstall
```

数据库与程序目录请按需自行清理。
