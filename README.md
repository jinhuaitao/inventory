# 库存管理系统 (Inventory System)

[![CI](https://github.com/jinhuaitao/inventory/actions/workflows/ci.yml/badge.svg)](https://github.com/jinhuaitao/inventory/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

一个用 **Go 语言** 编写的、功能完善的库存管理系统。使用 **SQLite** 存储数据、**服务端渲染 HTML** 呈现界面，编译后是**单个可执行文件**，无需任何外部依赖即可运行。

## ✨ 特性

### 账号与安全

- **注册账号** — 用户名 / 邮箱 / 密码，含密码强度实时提示与合法性校验
- **找回密码（安全问题）** — 注册时自行编写 3 个安全问题，忘记密码时作答即可自助重置
- **登录限流** — 连续失败 5 次锁定 15 分钟（可配置），找回密码答题同样受限流保护
- **密码加密** — bcrypt 哈希存储，永不落库明文；安全问题的答案同样只存摘要
- **答案容错** — 校验前自动归一化（忽略大小写与前后空格），不会因输入习惯而失败
- **会话管理** — 服务端会话 + SHA-256 令牌摘要，可在个人中心查看/踢出登录设备
- **CSRF 防护** — 双重提交令牌（已登录比对会话，未登录用 cookie）
- **安全响应头** — CSP、`X-Frame-Options`、`X-Content-Type-Options`、`Referrer-Policy` 等
- **三级角色权限** — `admin`（管理员）/ `manager`（仓管员）/ `viewer`（只读）

### 库存业务

- **商品管理** — SKU、分类、供应商、成本价/售价、库存上下限、单位、多维度筛选排序、分页
- **分类 / 供应商** — 完整增删改查，删除时自动解除商品关联
- **入库 / 出库** — 单据化操作，实时校验库存，库存不足直接拒绝并回滚
- **库存盘点** — 直接设定实际数量，自动计算盘盈盘亏
- **库存流水** — 每次变动留痕（操作人、数量、前后值、备注、时间），支持按类型/商品/日期筛选
- **库存预警** — 低于安全库存自动高亮，独立预警页面
- **数据看板** — 6 项核心指标、14 天出入库趋势图、分类价值分布、库存价值 TOP8、出库排行、最近操作
- **报表与导出** — 报表页支持自定义时间区间，商品/流水均可导出 CSV（带 BOM，Excel 中文不乱码）
- **审计保护** — 有流水的商品/用户禁止物理删除，只能归档或禁用

### 系统更新

- **版本检查** — 对接 GitHub Releases API，启动后每 6 小时自动检查（间隔可配置）
- **一键自更新** — 在管理界面直接下载新版本、替换可执行文件并重启服务
- **两种发布形态** — 同时支持 `tar.gz` / `zip` 归档与裸可执行文件；裸二进制在落盘前会校验文件头是否匹配当前平台
- **完整性校验** — 下载后比对 SHA-256：优先取发布中的 `checksums.txt`，缺失时回退到 GitHub 附件摘要，不通过立即中止，绝不替换文件
- **自动备份** — 替换前把当前版本改名为 `<程序名>.old`，随时可手动回滚
- **防 SSRF** — 只允许从 GitHub 官方域名下载，拒绝任何其他主机与明文 HTTP
- **命令行巡检** — `--check-update` 可在部署脚本或定时任务里检查版本

### 工程化

- **零 CGO** — 使用 `modernc.org/sqlite` 纯 Go 驱动，交叉编译无障碍
- **单文件部署** — 模板与静态资源通过 `embed.FS` 嵌入二进制
- **优雅关闭** — 监听 `SIGINT`/`SIGTERM`，等待请求处理完毕
- **结构化日志** — `log/slog`，开发环境文本、生产环境 JSON
- **健康探针** — `/healthz`（存活）与 `/readyz`（就绪，含数据库连通性）
- **后台清理** — 每小时清理过期会话、登录记录与找回密码记录
- **74 个单元测试** — 含并发写入一致性验证（10 协程 × 10 次入库，校验无丢失更新）

## 🚀 快速开始

### 方式一：下载预编译二进制（推荐）

前往 [Releases](https://github.com/jinhuaitao/inventory/releases) 下载对应平台压缩包：

| 平台 | 文件 |
| --- | --- |
| Linux x86_64 | `inventory-server-*-linux-amd64.tar.gz` |
| Linux ARM64 | `inventory-server-*-linux-arm64.tar.gz` |

```bash
tar -xzf inventory-server-*.tar.gz
chmod +x inventory-server-*
./inventory-server-*
```

### 方式二：从源码编译

```bash
git clone https://github.com/jinhuaitao/inventory.git
cd inventory
go mod tidy
go build -o inventory-server ./cmd/server
./inventory-server
```

### 访问

浏览器打开 <http://localhost:8080>

首次启动会自动创建数据库并写入演示数据，默认管理员账号：

| 用户名 | 密码 |
| --- | --- |
| `admin` | `Admin@12345` |

> ⚠️ **请登录后立即修改管理员密码。** 生产环境务必通过 `INVENTORY_ADMIN_PASSWORD` 指定强密码。

### 命令行参数

```bash
./inventory-server --version        # 打印版本与构建信息
./inventory-server --check-update   # 检查是否有新版本后退出
```

## 🔑 找回密码

本系统**不依赖任何邮件服务**，改用**安全问题**验证身份。

### 注册时

注册页面会要求填写 3 组「问题 + 答案」，问题文本完全由你自己编写，例如：

| # | 问题 | 答案 |
| --- | --- | --- |
| 1 | 我小学班主任的姓名是？ | 张老师 |
| 2 | 我第一辆自行车的颜色是？ | 蓝色 |
| 3 | 我母亲的出生城市是？ | 杭州 |

答案不区分大小写，前后的空格会被自动忽略。**问题文本不能重复，答案至少 2 个字。**

### 忘记密码时

1. 打开 `/forgot-password`，输入**用户名或邮箱**
2. 页面展示该账号设置的 3 个问题，逐题作答并设置新密码
3. 全部答对即重置成功，所有设备会被强制退出登录

答错会提示剩余次数，连续答错 5 次锁定 15 分钟。

### 修改安全问题

登录后进入 **个人中心 → 安全问题**，可随时修改。出于安全考虑，修改时需要验证当前密码，且答案不会回显。

### 兜底方案

如果连安全问题的答案也忘了，只能由管理员在 **用户管理** 中为该账号重置密码。

## ⚙️ 配置

全部配置通过环境变量注入，前缀为 `INVENTORY_`。

### 基础

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `INVENTORY_APP_NAME` | `库存管理系统` | 站点名称 |
| `INVENTORY_ENV` | `development` | `development` / `production` |
| `INVENTORY_ADDR` | `:8080` | 监听地址 |
| `INVENTORY_BASE_URL` | `http://localhost:8080` | 对外访问地址 |
| `INVENTORY_DATA_DIR` | `data` | 数据目录 |
| `INVENTORY_DB_PATH` | `data/inventory.db` | SQLite 数据库文件路径 |
| `INVENTORY_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

### 会话与安全

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `INVENTORY_SESSION_SECRET` | 开发环境自动生成 | **生产环境必填**，至少 16 字符的随机字符串 |
| `INVENTORY_SESSION_LIFETIME` | `12h` | 会话有效期 |
| `INVENTORY_REMEMBER_LIFETIME` | `720h` | 「记住我」有效期 |
| `INVENTORY_MAX_LOGIN_ATTEMPTS` | `5` | 登录失败 / 答题失败次数上限 |
| `INVENTORY_LOCKOUT_WINDOW` | `15m` | 锁定时长 |

### 账号策略

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `INVENTORY_ALLOW_REGISTRATION` | `true` | 是否开放自助注册 |
| `INVENTORY_DEFAULT_ROLE` | `viewer` | 自助注册用户的默认角色（`viewer`/`manager`/`admin`） |
| `INVENTORY_ADMIN_USERNAME` | `admin` | 初始化管理员用户名 |
| `INVENTORY_ADMIN_EMAIL` | `admin@example.com` | 初始化管理员邮箱 |
| `INVENTORY_ADMIN_PASSWORD` | `Admin@12345` | 初始化管理员密码 |

### 在线更新

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `INVENTORY_UPDATE_ENABLED` | `true` | 是否启用在线更新 |
| `INVENTORY_UPDATE_REPO` | 构建时注入 | 更新仓库，形如 `owner/repo`；留空则整个功能关闭 |
| `INVENTORY_UPDATE_INTERVAL` | `6h` | 自动检查间隔 |
| `INVENTORY_UPDATE_TOKEN` | 空 | 可选，访问私有仓库或规避 API 限流 |

> 💡 官方 Release 的二进制已通过 `-ldflags` 注入 `main.updateRepo` 与 `main.version`，开箱即可检查更新。
> 自行编译时可以用同样的方式指定（`main.version` 务必填语义化版本号，否则程序会把自己识别为开发构建而拒绝比较）：
>
> ```bash
> go build -ldflags "-X main.version=v1.0.0 -X main.updateRepo=你的用户名/inventory" \
>   -o inventory-server ./cmd/server
> ```

### 支持的安装包命名

更新器按以下优先级在 Release 附件中挑选安装包，三种命名都能识别：

| 优先级 | 形态 | 示例 |
| --- | --- | --- |
| 1 | 归档（含系统名） | `inventory-server-v1.0.0-linux-amd64.tar.gz`（Windows 为 `.zip`） |
| 2 | 裸可执行文件（含系统名） | `inventory-server-linux-amd64` |
| 3 | 裸可执行文件（仅架构名） | `inventory-server-amd64` |

第 3 种命名不含操作系统信息，因此**仅在 Linux 上启用**，避免 macOS / Windows 误装 Linux 二进制；
真正落盘前还会再校验一次可执行文件头（ELF / PE / Mach-O）。

## 🔄 系统更新怎么用

1. 用管理员账号登录，进入 **系统更新** 页面（侧边栏「分析与管理」分组）
2. 页面会显示当前版本、最新版本、发布时间与该版本的更新说明
3. 有更新时点击 **立即更新**，系统会：
   - 下载当前平台对应的安装包
   - 比对 SHA-256（优先取发布中的 `checksums.txt`，缺失时回退到 GitHub 附件摘要），**校验不通过立即中止**
   - 解出可执行文件（归档则解压，裸二进制则校验文件头后直接落盘），把当前文件改名为 `<程序名>.old` 作为备份
   - 原子替换并自动重启服务（通常几秒钟）
4. 更新完成后页面会自动刷新，可确认版本号已变更

**回滚方式**：停止服务，把 `<程序名>.old` 覆盖回 `<程序名>`，重新启动即可。

**平台差异**：Windows 无法替换正在运行的程序，系统会把新版本下载为 `<程序名>.new` 并提示手动完成替换。

## 🐳 生产部署示例

```bash
export INVENTORY_ENV=production
export INVENTORY_ADDR=:8080
export INVENTORY_BASE_URL=https://inventory.example.com
export INVENTORY_SESSION_SECRET="$(openssl rand -hex 32)"
export INVENTORY_ADMIN_PASSWORD='YourStrongPassword123'
export INVENTORY_ALLOW_REGISTRATION=false
export INVENTORY_DEFAULT_ROLE=viewer

# 可选：仅当需要覆盖编译时注入的更新仓库时才设置
# export INVENTORY_UPDATE_REPO=jinhuaitao/inventory

./inventory-server
```

> 💡 使用官方 Release 的二进制时，更新仓库已由 CI 通过 `-ldflags` 注入，
> **无需**再设置 `INVENTORY_UPDATE_REPO`。自行编译的二进制则必须设置，
> 否则「系统更新」功能会显示为未启用。

建议在反向代理（Nginx / Caddy）后运行并启用 HTTPS —— 系统检测到 HTTPS 时会自动加上 HSTS 头，
同时会话 cookie 会带 `Secure` 标记。

> ⚠️ 自更新需要写权限：请确保运行用户对可执行文件所在目录有写权限，
> 否则会返回「备份当前版本失败」的明确提示，且不会改动任何文件。

## 📦 技术栈

| 层面 | 选型 |
| --- | --- |
| 语言 | Go 1.27 |
| 数据库 | SQLite（`modernc.org/sqlite`，纯 Go、无 CGO） |
| HTTP | 标准库 `net/http`（Go 1.22+ 方法+路径模式路由） |
| 模板 | 标准库 `html/template` + `embed.FS` |
| 密码 | `golang.org/x/crypto/bcrypt` |
| 日志 | 标准库 `log/slog` |
| 更新 | GitHub Releases API + 标准库 `archive/tar`、`archive/zip` |
| 前端 | 手写 HTML / CSS / 原生 JS，零框架零依赖 |

## 📁 项目结构

```
.
├── cmd/server/           # 程序入口
├── internal/
│   ├── auth/             # 会话管理
│   ├── config/           # 环境变量配置加载
│   ├── database/         # 连接、迁移、种子数据
│   ├── handlers/         # HTTP 处理器（按业务分文件）
│   ├── middleware/       # 中间件（恢复、日志、安全头、认证、CSRF）
│   ├── models/           # 领域模型
│   ├── services/         # 业务逻辑与 SQL（刻意不用 ORM）
│   ├── updater/          # 版本检查与自更新
│   └── utils/            # 模板渲染、格式化、校验、分页、CSV
├── web/
│   ├── templates/        # 服务端渲染模板
│   └── static/           # CSS / JS / 图标
└── .github/workflows/    # CI 与 Release 流水线
```

## 🛠️ 开发

```bash
make help        # 查看全部可用命令
make run         # 本地运行
make test        # 运行单元测试
make race        # 带竞态检测运行测试
make check       # 格式检查 + 静态分析 + 测试
make build       # 编译当前平台
make release-artifacts   # 生成与 CI 一致的发布产物（Linux amd64/arm64 + checksums.txt）
```

### 代码树体检

编译报一堆 `undefined`，或者推上去之后 CI 一直不跑？先执行体检脚本：

```bash
./scripts/doctor.sh
```

它会依次检查四件事，并在发现问题时直接给出修复命令：

1. **必需文件是否齐全** —— 缺 `.gitignore`、`.github/workflows/` 会导致 CI 完全不触发；
2. **是否有已废弃文件残留** —— 旧版本被删除的文件若仍留在目录里，会让 `go build` 报错；
3. **`.gitignore` 是否误伤源码**，以及构建产物是否被误纳入版本控制；
4. **`gofmt` / `go vet` / `go build`** 是否通过。

> 💡 **更新代码请用 `git pull`，不要用 GitHub 网页的「上传文件」。**
> 网页上传只能新增/覆盖文件、无法删除文件，而且以 `.` 开头的隐藏文件与目录
> （`.gitignore`、`.github/`）在拖拽时常常被系统跳过。结果是旧文件删不掉、新文件传不全，
> 于是出现「编译失败 + CI 不触发」的组合症状。

### 发布到自己的 GitHub

`scripts/publish.sh` 会替换 README 中的仓库地址占位符、配置提交身份，并在检测到已登录的
`gh` CLI 时直接创建远程仓库并推送：

```bash
./scripts/publish.sh <你的GitHub用户名> inventory <你的提交邮箱>
```

## 🔄 持续集成与发布

- **CI**（`.github/workflows/ci.yml`）— 每次推送与 PR 触发，依次执行：
  1. **代码检查** — `go mod tidy` 差异、`gofmt`、`go vet`
  2. **编译与测试** — 构建全部包、`go test -race`、输出覆盖率
  3. **端到端冒烟测试** — 真实启动服务，校验健康探针与关键页面

- **Release**（同一个 `ci.yml` 内的 `release` 作业）— 推送到 `main` / `master`
  或手动触发时自动发版，只产出 **Linux 的两个架构**：

  1. `test` 作业先依据历史 Tag 算出本次版本号（`v1.0.01` → `v1.0.02` …）
  2. 用该版本号交叉编译 `linux/amd64` 与 `linux/arm64`，并通过 `-ldflags` 注入
     `main.version` 与 `main.updateRepo`，**两者缺一都会导致更新功能不可用**
  3. 生成 `checksums.txt`（SHA-256）
  4. `release` 作业复用同一个版本号创建 GitHub Release，上传两个二进制与校验和文件

  > ⚠️ 版本号必须在**编译之前**算出来。若直接用 `github.ref_name`，在推送 `main`
  > 的场景下取到的值是分支名 `main` 而不是 Tag，发布出去的程序会把自己识别为开发构建，
  > 从而永远无法比较版本。

## 🔒 安全说明

- 数据库统一以 **UTC** 存储时间，界面按 **UTC+8** 展示
- 会话 cookie 为 `HttpOnly` + `SameSite=Lax`，生产 HTTPS 下额外加 `Secure`
- 所有 SQL 使用参数化查询；排序字段走白名单，杜绝注入
- 模板自动转义；Release 说明在转义后才按纯文本渲染
- 登录后跳转地址经 `sanitizeNext` 过滤，防止开放重定向
- 「最后一个启用状态的管理员」受保护，不可降级、禁用或删除
- 安全问题答案用 bcrypt 哈希存储，校验时遍历全部题目（不短路），避免通过响应时间推测答对数量
- 自更新只允许从 GitHub 官方域名下载，强制 HTTPS，并限制安装包体积上限
- 自更新必须先通过 SHA-256 校验才会替换文件，替换失败自动回滚
- 校验和优先取发布中的 `checksums.txt`，缺失时回退到 GitHub API 的附件摘要（`asset.digest`）；
  两者都没有时直接中止更新，绝不降级为「不校验」
- 裸二进制发布形态在落盘前会校验可执行文件头（ELF / PE / Mach-O），防止把其它系统的
  可执行文件装到当前平台

### 已知权衡

使用安全问题替代邮件找回，意味着**第一步需要展示问题**，因此无法像邮箱方式那样完全隐藏
「该账号是否存在」。这是安全性（自助找回的可用性）与隐私性之间的取舍，系统通过以下方式降低风险：

- 对同一标识的查询与答题做频率限制（默认 15 分钟内 5 次）
- 账号被禁用时与不存在时返回相同的提示
- 答案永不回显，问题文本不做模糊匹配

如果对账号枚举风险更敏感，可以关闭自助注册（`INVENTORY_ALLOW_REGISTRATION=false`），
由管理员统一开设账号。

## 📄 许可证

[MIT](LICENSE)
