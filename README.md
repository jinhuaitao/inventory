# 库存管理系统 (Inventory System)

![CI](https://github.com/jinhuaitao/inventory/actions/workflows/ci.yml/badge.svg) ![Go Version](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go) ![License](https://img.shields.io/badge/License-MIT-blue.svg)

一个用 **Go 语言** 编写的、功能完善的库存管理系统。使用 **SQLite** 存储数据、**服务端渲染 HTML** 呈现界面，编译后是**单个可执行文件**，无需任何外部依赖即可运行。

## ✨ 特性

### 账号与安全

- **注册账号** — 用户名 / 邮箱 / 密码，含密码强度实时提示与合法性校验
- **注册开关可运行时调整** — 管理员在「用户管理」页一键开启 / 关闭自助注册，  
  关闭后注册入口与 `/register` 同时失效；开关即定论，设置入库、重启保留（见「账号策略」）
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
- **库存流水** — 每次变动留痕（操作人、数量、前后值、备注、时间），支持按类型/商品/日期筛选；  
  导出上限 5 万行，超出时在文件末尾明确提示而不是静默截断
- **库存预警** — 低于安全库存自动高亮（**含已缺货**），独立预警页面；  
  页面顶部的构成统计走聚合查询，覆盖**全部**预警商品而非当前页
- **数据看板** — 6 项核心指标、14 天出入库趋势图、分类价值分布、库存价值 TOP8、出库排行、最近操作
- **报表与导出** — 报表页支持自定义时间区间，商品/流水均可导出 CSV（带 BOM，Excel 中文不乱码）
- **审计保护** — 有流水的商品 / 供应商禁止物理删除，只能归档或停用；  
  应用层检查之外，数据库外键也设为 `ON DELETE RESTRICT` 兜底（见「安全说明」）

### 系统更新

- **版本检查** — 对接 GitHub Releases API，启动后每 6 小时自动检查（间隔可配置）
- **一键自更新** — 在管理界面直接下载新版本、替换可执行文件并重启服务
- **两种发布形态** — 同时支持 `tar.gz` / `zip` 归档与裸可执行文件；裸二进制在落盘前会校验文件头是否匹配当前平台
- **完整性校验** — 下载后比对 SHA-256：优先取发布中的 `checksums.txt`，缺失时回退到 GitHub 附件摘要，不通过立即中止，绝不替换文件
- **自动备份** — 替换前把当前版本改名为 `<程序名>.old`，随时可手动回滚
- **就地重启** — 用 `syscall.Exec` 替换进程映像，PID 不变，systemd / OpenRC 都能正确跟踪
- **防 SSRF** — 只允许从 GitHub 官方域名下载，拒绝任何其他主机与明文 HTTP
- **命令行巡检** — `--check-update` 可在部署脚本或定时任务里检查版本

### 数据维护

- **网页备份** — 管理界面一键生成备份（`VACUUM INTO` 读事务快照），**不用停服务**；产出已合并 WAL 的单文件，可随时下载到本地
- **上传恢复** — 直接在浏览器上传 `.db` 恢复数据，无需 SSH；上传时先做完整性检查，不通过立即拒绝且不落盘
- **两步换库** — 恢复文件先暂存，**重启时才换入**，避免运行中的连接指向被替换的 inode；原库自动归档为 `inventory.db.before-restore-<时间戳>`
- **WAL 残留自动清理** — 换库时一并删掉旧的 `-wal` / `-shm`，防止旧 WAL 套到新库上把数据写坏
- **清理演示数据** — 页面内先预览将删除的条数，确认后执行，且**自动先做一次安全备份**
- **备份轮转** — 自动命名的备份按保留份数自动清理，手工改名或放进去的备份不会被误删

### 工程化

- **零 CGO** — 使用 `modernc.org/sqlite` 纯 Go 驱动，交叉编译无障碍
- **单文件部署** — 模板与静态资源通过 `embed.FS` 嵌入二进制
- **开箱可用的服务定义** — 自带 systemd 单元与 OpenRC 服务脚本，一条命令装成系统服务
- **演示数据可关可清** — 生产环境默认不写入；历史遗留的演示数据可用 `--purge-demo-data` 精确清理
- **安全备份与迁移** — `--backup-db` 基于 `VACUUM INTO` 取读事务快照，**不用停服务**，产出的单文件已合并 WAL，直接拷走即可；安装脚本默认配好每日自动备份与轮转
- **优雅关闭** — 监听 `SIGINT`/`SIGTERM`，等待请求处理完毕
- **结构化日志** — `log/slog`，开发环境文本、生产环境 JSON
- **健康探针** — `/healthz`（存活）与 `/readyz`（就绪，含数据库连通性）；两者**都不加载会话**，  
  数据库出问题时存活探针依然可用，不会被误判为进程已死而触发重启
- **后台清理** — 每小时清理过期会话、登录记录与找回密码记录
- **300+ 单元测试** — 含并发写入一致性验证（10 协程 × 10 次入库，校验无丢失更新）；  
  关键安全修复另用**变异测试**逐条验证（把缺陷改回去，确认测试真的会失败）

## 🚀 快速开始

### 方式一：一键安装（推荐）

```
curl -o inventory.sh https://raw.githubusercontent.com/jinhuaitao/inventory/master/inventory.sh && chmod +x inventory.sh && ./inventory.sh
```

### 方式二：下载预编译二进制

前往 [Releases](https://github.com/jinhuaitao/inventory/releases) 下载对应架构的**裸可执行文件**（静态链接、无 CGO，glibc 与 musl 均可直接运行）：

| 平台           | 文件                       |
| ------------ | ------------------------ |
| Linux x86_64 | `inventory-server-amd64` |
| Linux ARM64  | `inventory-server-arm64` |
| 校验和          | `checksums.txt`          |

```bash
curl -fLO https://github.com/jinhuaitao/inventory/releases/latest/download/inventory-server-amd64
chmod +x inventory-server-amd64
./inventory-server-amd64
```

生产环境建议直接用一键脚本安装为系统服务（自动生成配置与随机会话密钥）：

```bash
# Debian / Ubuntu（systemd）
sudo ./deploy/install.sh

# Alpine Linux（OpenRC）
sudo ./deploy/install.sh
```

详见 [deploy/README.md](deploy/README.md)。

### 方式三：从源码编译

```bash
git clone https://github.com/jinhuaitao/inventory.git
cd inventory
go mod tidy
go build -o inventory-server ./cmd/server
./inventory-server
```

### 访问

浏览器打开 <http://localhost:8080>

首次启动时，如果数据库为空，会自动创建默认管理员账号；**开发环境**还会额外写入一组  
演示数据（4 个分类、3 个供应商、12 个商品）方便试用：

| 用户名     | 密码            |
| ------- | ------------- |
| `admin` | `Admin@12345` |

> ⚠️ **请登录后立即修改管理员密码。** 生产环境务必通过 `INVENTORY_ADMIN_PASSWORD` 指定强密码。

演示数据的写入受 `INVENTORY_SEED_DEMO_DATA` 控制：**不设置时生产环境为关闭、其他环境为开启**。  
若库里已经存在演示数据，可用下面的命令清理（先预览，再执行）：

```bash
./inventory-server --purge-demo-data --dry-run   # 只统计，不改动
./inventory-server --purge-demo-data             # 实际清理
```

清理默认**只删带演示标记（`is_demo=1`）的行**，管理员账号与自建数据不受影响；  
仍被商品引用的分类 / 供应商会被保留并列出。

旧版本数据库（加标记之前写入的演示数据）里的商品没有标记，命令会把它们列出来  
但**不会删除**，需要确认后加 `--purge-legacy-demo` 才会一并处理。之所以不默认按  
SKU 删除：SKU 是用户可以自由填写的字段，无条件按 SKU 匹配会连带删掉用户自建的  
商品及其全部库存流水，且不可逆。

### 命令行参数

```bash
./inventory-server --version                     # 打印版本与构建信息
./inventory-server --check-update                # 检查是否有新版本后退出
./inventory-server --purge-demo-data             # 清理内置演示数据后退出
./inventory-server --purge-demo-data --dry-run   # 只预览将要删除的数据
./inventory-server --purge-demo-data --purge-legacy-demo  # 一并清理旧版遗留（无标记）演示数据
./inventory-server --backup-db ./backups         # 备份数据库（不用停服务）
./inventory-server --backup-db ./backups --keep 7  # 备份并只保留最近 7 份
```

## 🔑 找回密码

本系统**不依赖任何邮件服务**，改用**安全问题**验证身份。

### 注册时

注册页面会要求填写 3 组「问题 + 答案」，问题文本完全由你自己编写，例如：

| # | 问题           | 答案  |
| - | ------------ | --- |
| 1 | 我小学班主任的姓名是？  | 张老师 |
| 2 | 我第一辆自行车的颜色是？ | 蓝色  |
| 3 | 我母亲的出生城市是？   | 杭州  |

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

| 变量                    | 默认值                     | 说明                                  |
| --------------------- | ----------------------- | ----------------------------------- |
| `INVENTORY_APP_NAME`  | `库存管理系统`                | 站点名称                                |
| `INVENTORY_ENV`       | `development`           | `development` / `production`        |
| `INVENTORY_ADDR`      | `:8080`                 | 监听地址                                |
| `INVENTORY_BASE_URL`  | `http://localhost:8080` | 对外访问地址                              |
| `INVENTORY_DATA_DIR`  | `data`                  | 数据目录                                |
| `INVENTORY_DB_PATH`   | `data/inventory.db`     | SQLite 数据库文件路径                      |
| `INVENTORY_LOG_LEVEL` | `info`                  | `debug` / `info` / `warn` / `error` |

> 💡 `INVENTORY_DB_PATH` 里如果出现 `?` `#` `%`，程序会在拼接 DSN 前做百分号编码。  
> 不编码的话，路径中这些字符之后的部分会被 SQLite 当成连接参数，实际打开的可能是  
> 另一个文件 —— 表现就是「换了路径之后数据全没了」，而目录里还多出一个空库。

### 会话与安全

| 变量                             | 默认值                   | 说明                                                          |
| ------------------------------ | --------------------- | ----------------------------------------------------------- |
| `INVENTORY_SESSION_SECRET`     | 开发环境自动生成              | **生产环境必填**，至少 16 字符的随机字符串                                   |
| `INVENTORY_SESSION_LIFETIME`   | `12h`                 | 会话有效期                                                       |
| `INVENTORY_REMEMBER_LIFETIME`  | `720h`                | 「记住我」有效期                                                    |
| `INVENTORY_MAX_LOGIN_ATTEMPTS` | `5`                   | 同一「账号 + 来源 IP」的失败上限；同一账号跨来源的兜底上限是它的 10 倍                    |
| `INVENTORY_LOCKOUT_WINDOW`     | `15m`                 | 失败计数的统计窗口（窗口过后自动解锁）                                         |
| `INVENTORY_TRUSTED_PROXIES`    | `127.0.0.1/8,::1/128` | 允许采信 `X-Forwarded-For` / `X-Real-IP` 的网段，逗号分隔，支持 CIDR 与裸 IP |

> ⚠️ 只有在请求**确实来自这些网段**时才会采信转发头。代理在别的机器或容器里时，  
> 必须把它的网段加进来，否则日志与审计里记录的会是代理自己的地址。  
> **不要**配置成 `0.0.0.0/0` —— 那等于让任何人都能用一个请求头伪造来源 IP。

### 账号策略

| 变量                             | 默认值                 | 说明                              |
| ------------------------------ | ------------------- | ------------------------------- |
| `INVENTORY_ALLOW_REGISTRATION` | `true`              | 自助注册的**初始**状态；页面上一旦设置过就以页面为准     |
| `INVENTORY_DEFAULT_ROLE`       | `viewer`            | 自助注册用户的默认角色（`viewer`/`manager`） |
| `INVENTORY_ADMIN_USERNAME`     | `admin`             | 初始化管理员用户名                       |
| `INVENTORY_ADMIN_EMAIL`        | `admin@example.com` | 初始化管理员邮箱                        |
| `INVENTORY_ADMIN_PASSWORD`     | `Admin@12345`       | 初始化管理员密码                        |
| `INVENTORY_SEED_DEMO_DATA`     | 随环境（生产 `false`）     | 数据库为空时是否写入内置演示数据                |

> ⚠️ `INVENTORY_DEFAULT_ROLE` **不接受 `admin`**：开放注册 + 默认管理员等于  
> 「任何能访问注册页的人自助获得最高权限」。填成 `admin` 会让程序**拒绝启动**，  
> 而不是静默放行。确实需要管理员时，请先注册再由管理员提权。

#### 自助注册开关

自助注册可以在**运行时**调整，不必改配置重启：

1. 管理员进入 **用户管理**，页面顶部的「自助注册」卡片显示当前状态；
2. 点「开启自助注册 / 关闭自助注册」即刻生效 —— 登录页的注册入口、  
   `/register` 的准入判断都会同步改变；
3. 设置写入数据库（`settings` 表），重启后依然保留。

**这是一个纯粹的开关：点一下，状态即定论。**  
`INVENTORY_ALLOW_REGISTRATION` 只决定**初始状态**（首次部署、以及页面从未设置过时的取值）；  
管理员在页面上做出选择之后，就以那个选择为准，不会再被环境变量翻回去 ——  
页面上也不会出现「我到底设没设过」这种说不清的情况。

> 为什么允许页面覆盖环境变量：能进用户管理页的管理员本来就能直接建号，  
> 放开这个开关不会带来额外的权限提升，却省掉了「登服务器改配置 + 重启」这一整轮操作。
>
> 关闭注册后，直接 POST `/register` 同样会被拒绝（不是只藏起入口），  
> 且**立即生效**，不需要等下次重启。
>
> ⚠️ 开关存在数据库里，所以它会**跟着备份一起被恢复**。从一份旧备份恢复数据时，  
> 注册状态会回到那份备份当时的取值 —— 恢复完顺手看一眼这张卡片。

### 数据库备份

| 变量                      | 默认值              | 说明                         |
| ----------------------- | ---------------- | -------------------------- |
| `INVENTORY_BACKUP_DIR`  | `<数据目录>/backups` | 备份目录；网页「数据维护」与每日定时备份共用同一目录 |
| `INVENTORY_BACKUP_KEEP` | `14`             | 自动命名备份的保留份数，`0` 表示不自动清理    |

> `INVENTORY_BACKUP_KEEP` 只清理严格匹配 `inventory-YYYYMMDD-HHMMSS.db` 的文件，  
> 手工改名或放进去的备份不会被误删。

### 在线更新

| 变量                          | 默认值    | 说明                             |
| --------------------------- | ------ | ------------------------------ |
| `INVENTORY_UPDATE_ENABLED`  | `true` | 是否启用在线更新                       |
| `INVENTORY_UPDATE_REPO`     | 构建时注入  | 更新仓库，形如 `owner/repo`；留空则整个功能关闭 |
| `INVENTORY_UPDATE_INTERVAL` | `6h`   | 自动检查间隔                         |
| `INVENTORY_UPDATE_TOKEN`    | 空      | 可选，访问私有仓库或规避 API 限流            |

> 💡 官方 Release 的二进制已通过 `-ldflags` 注入 `main.updateRepo` 与 `main.version`，开箱即可检查更新。  
> 自行编译时可以用同样的方式指定（`main.version` 务必填语义化版本号，否则程序会把自己识别为开发构建而拒绝比较）：
>
> ```bash
> go build -ldflags "-X main.version=v1.0.0 -X main.updateRepo=你的用户名/inventory" \
>   -o inventory-server ./cmd/server
> ```

### 支持的安装包命名

更新器按以下优先级在 Release 附件中挑选安装包，三种命名都能识别：


| 优先级 | 形态           | 示例                                                             |
| --- | ------------ | -------------------------------------------------------------- |
| 1   | 归档（含系统名）     | `inventory-server-v1.0.0-linux-amd64.tar.gz`（Windows 为 `.zip`） |
| 2   | 裸可执行文件（含系统名） | `inventory-server-linux-amd64`                                 |
| 3   | 裸可执行文件（仅架构名） | `inventory-server-amd64`                                       |

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

### 重启是怎么做到的

替换文件之后，程序用 `syscall.Exec` **就地替换进程映像**：

- **PID 保持不变**，因此 systemd 与 OpenRC 都能继续跟踪同一个进程，不会出现「服务已退出」  
  的误判，也不会留下重复进程；
- 不需要 `Restart=always` 之类的配合，服务定义里只保留异常退出时的兜底重启；
- 监听端口由内核在 exec 时释放并重新绑定，无需外部进程管理器介入。

> ⚠️ 前提是**运行用户对可执行文件所在目录有写权限**。仓库提供的 systemd 单元已通过  
> `ReadWritePaths=/opt/inventory` 放行；若你换了安装路径，记得同步修改。  
> 权限不足时会返回「备份当前版本失败（请确认对 ... 有写权限）」，且不会改动任何文件。

**回滚方式**：停止服务，把 `<程序名>.old` 覆盖回 `<程序名>`，重新启动即可。

**平台差异**：Windows 无法替换正在运行的程序，系统会把新版本下载为 `<程序名>.new` 并提示手动完成替换。

## 🗄️ 数据维护怎么用

用管理员账号登录，进入 **数据维护** 页面（侧边栏「分析与管理」分组，紧邻「系统更新」）。  
不方便 SSH 登录服务器时，这一页就能完成备份、恢复与清理。

### 备份

点 **立即备份** 即生成一份 `inventory-<时间戳>.db`，写入 `INVENTORY_BACKUP_DIR`  
（默认 `<数据目录>/backups`，部署脚本会设为 `/var/backups/inventory`）。  
列表里可以直接 **下载** 或 **删除**；页面顶部还会显示备份数量与最近一次备份时间。

> 💡 备份与数据库在同一块磁盘上，**不等于异地容灾**。请定期把下载到的 `.db`  
> 复制到别的机器或对象存储。

### 恢复

1. 选择并上传一个 `.db` 备份
2. 系统先做完整性检查（`integrity_check` + 各表行数），**不通过直接拒绝，不落盘**
3. 通过后暂存为 `inventory.db.restore-pending`，页面提示稍后自动重启生效
4. 服务重启时（在打开数据库**之前**）完成换库：
   - 当前库归档为 `inventory.db.before-restore-<时间戳>`
   - 清掉残留的 `inventory.db-wal` / `-shm`
   - 暂存文件换入
5. 确认数据正确后，可自行删除归档文件

> ⚠️ 恢复会**整体替换**当前数据库。旧库虽被自动归档，仍建议先手动备份一次。  
> 上传的文件若有问题，重启时会被改名为 `inventory.db.restore-failed` 保留证据，  
> **原库不受影响**，服务照常启动。

### 清理演示数据

页面会先显示将删除的条数（商品 / 库存流水 / 分类 / 供应商）。确认后点执行，  
系统会**先自动做一次安全备份**再删除，备份名会出现在上方的备份列表中。

> 命令行版本（`--purge-demo-data`）与网页版规则完全一致：只删带演示标记的数据，  
> 仍被商品引用的分类 / 供应商会保留。网页上还会列出「SKU 与内置演示编号相同但  
> 没有演示标记」的商品（可能是您自建的商品，也可能是旧版遗留数据），  
> 确认属于后者时用命令行加 `--purge-legacy-demo` 处理。  
> 详见 [deploy/README.md 第 5 节](deploy/README.md#5-清理演示数据)。

## 🐳 生产部署示例

### Debian / Ubuntu（systemd）

```bash
sudo ./deploy/install.sh
systemctl status inventory
journalctl -u inventory -f
```

### Alpine Linux（OpenRC）

```bash
sudo ./deploy/install.sh
rc-service inventory status
tail -f /var/log/inventory/inventory.log
```

脚本会自动创建 `inventory` 服务账号、安装二进制到 `/opt/inventory/bin/`、  
生成带随机会话密钥的配置文件、注册开机自启，并配置好**每日自动备份**  
（`/var/backups/inventory`，保留 14 份）。完整说明见  
[deploy/README.md](deploy/README.md)。

### 备份与换机器

数据库开启了 WAL 模式，**运行中直接 `cp inventory.db` 可能拷到一个空库** ——  
最新数据还在 `inventory.db-wal` 里没合并。请用 `--backup-db`：

```bash
./inventory-server --backup-db /var/backups/inventory   # 不用停服务
```

它内部走 SQLite 的 `VACUUM INTO`，取读事务快照，产出已合并 WAL 的单文件，  
并会立刻回读校验（`integrity_check` + 各表行数）。换机器的完整步骤见  
[deploy/README.md 第 6 节](deploy/README.md#6-数据库备份与迁移换机器看这里)。

### 手动运行

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

## 📦 技术栈

| 层面   | 选型                                                    |
| ---- | ----------------------------------------------------- |
| 语言   | Go 1.27                                               |
| 数据库  | SQLite（`modernc.org/sqlite`，纯 Go、无 CGO）               |
| HTTP | 标准库 `net/http`（Go 1.22+ 方法+路径模式路由）                    |
| 模板   | 标准库 `html/template` + `embed.FS`                      |
| 密码   | `golang.org/x/crypto/bcrypt`                          |
| 日志   | 标准库 `log/slog`                                        |
| 更新   | GitHub Releases API + 标准库 `archive/tar`、`archive/zip` |
| 前端   | 手写 HTML / CSS / 原生 JS，零框架零依赖                          |

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
├── deploy/               # 部署资产
│   ├── install.sh        #   一键安装（自动识别 systemd / OpenRC）
│   ├── systemd/          #   systemd 单元
│   └── openrc/           #   OpenRC 服务脚本与配置模板
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
- 登录后跳转地址经 `sanitizeNext` 过滤，防止开放重定向 —— 解析后要求**同站相对路径**，  
  且路径中不允许出现任何 ASCII 空白或控制字符（含 Tab / 换行 / 空格 / `%20`）
- 「最后一个启用状态的管理员」受保护，不可降级、禁用或删除
- 安全问题答案用 bcrypt 哈希存储，校验时遍历全部题目（不短路），避免通过响应时间推测答对数量
- 自更新只允许从 GitHub 官方域名下载，强制 HTTPS，并限制安装包体积上限
- 自更新必须先通过 SHA-256 校验才会替换文件，替换失败自动回滚
- 校验和优先取发布中的 `checksums.txt`，缺失时回退到 GitHub API 的附件摘要（`asset.digest`）；  
  两者都没有时直接中止更新，绝不降级为「不校验」
- 裸二进制发布形态在落盘前会校验可执行文件头（ELF / PE / Mach-O），防止把其它系统的  
  可执行文件装到当前平台
- 备份的下载与删除都先做文件名白名单校验（拒绝路径分隔符与 `..`，只接受 `.db`），  
  且校验解析后的绝对路径确实落在备份目录内，杜绝路径穿越
- 上传的备份必须先通过 SQLite 完整性检查（`integrity_check`）才允许暂存，  
  不合格立即拒绝且不写入磁盘
- 上传体积上限 512 MB，且在解析之前就用 `http.MaxBytesReader` 套住 body，  
  避免超大请求先把临时目录写满
- CSRF 校验区分 `multipart/form-data` 与 `urlencoded` —— `ParseForm` 不解析 multipart  
  body，若不单独处理会让所有文件上传表单的 `_csrf` 字段读不到而被误判为攻击
- **未登录的 `multipart` 请求在解析 body 之前就被拒绝** —— CSRF 中间件跑在鉴权之前，  
  否则任何人都能在没有任何凭据的情况下发起一次 512 MB 的上传把磁盘写满
- **`multipart` 的体积上限与 CSRF 令牌的来源无关** —— 无论令牌是从表单字段还是  
  `X-CSRF-Token` 头读取，限制都在**解析 body 之前**套上。只在「令牌来自表单字段」  
  这条分支里加限制的话，改用一个请求头就能绕过，上传表单会退化成磁盘填充器
- **来源 IP 只在请求确实来自可信代理时才采信转发头**（`INVENTORY_TRUSTED_PROXIES`，  
  默认只信任回环地址）。取值用 `X-Forwarded-For` 中**最右侧的不可信地址**：  
  反向代理是追加而非覆盖，取最左值等于让任何人用请求头伪造来源 IP
- **登录限流按「账号 + 来源 IP」为主、账号总量为兜底**（阈值 10 倍）。  
  单看账号总数就锁定的话，攻击者无需任何凭据、每 15 分钟发 5 次错误密码  
  就能把别人的账号永久锁死
- **被限流的请求只留痕、不计入失败次数**，否则攻击者持续发请求就能不断刷新  
  计数窗口，让锁定永远不过期
- **修改邮箱需要重新输入当前密码** —— 邮箱是找回密码的凭据，仅靠会话即可修改  
  的话，会话被窃取后攻击者能直接把找回通道指向自己
- **CSV 导出做公式注入防护** —— 商品名、SKU、备注等自由输入以 `=` `+` `-` `@`  
  开头时会被表格软件当公式求值，导出时统一加前缀转义（纯负数除外，避免破坏排序求和）。  
  判定前**先剥掉前导空白** —— 只检查首字符的话，一个前置空格就能让防护失效
- **审计保护是「应用层 + 数据库」两道** —— 删除前先检查是否已有库存流水，检查与删除  
  放在**同一个事务**里；同时 `stock_movements` 的外键设为 `ON DELETE RESTRICT`，  
  即使将来有人绕过服务层直接写 SQL，也不可能把有流水的商品或供应商删掉。  
  旧库启动时会自动重建该表以收紧外键（`ON DELETE CASCADE` / `SET NULL` 会让删除  
  商品时**静默抹掉审计记录**，这正是审计保护最不能接受的失败方式）
- **库存结存有上限校验** —— 不只校验单次录入数量，入库后的**结存**也必须在  
  `0 ~ 上限` 之间。只卡单次输入的话，连续两次大额入库就能让库存累加溢出成负数
- **盘点数未变化时拒绝写入** —— 提交一个与当前库存相同的数字会被拒绝并提示，  
  避免在流水里留下一堆 `delta = 0` 的噪音记录，把真正的调整淹没掉
- **「全部状态」筛选真的返回全部** —— 默认视图隐藏归档商品，但下拉里显式选择  
  「全部状态」时必须包含归档。两者共用同一个分支的话，一个叫「全部」的选项  
  却少给结果，比根本没有这个选项更容易误导人
- **低库存只有一个口径** —— 仪表盘徽标、预警列表条数、预警页的「已缺货 / 库存偏低」  
  三处统计必须严丝合缝：缺货是低库存中更严重的一档（**包含**关系而非并列），  
  归档商品一律不计入
- **演示数据清理默认只认 `is_demo` 标记**，不按 SKU 兜底删除 —— SKU 是用户可自由  
  填写的字段，按 SKU 匹配会误删用户自建商品及其全部库存流水（旧库兼容需显式  
  `--purge-legacy-demo`）
- **`INVENTORY_DEFAULT_ROLE` 拒绝 `admin`** —— 开放注册 + 默认管理员等于任何人  
  自助获得最高权限，这类配置直接让启动失败而不是静默放行
- **注册开关的请求参数按严格模式解析** —— 字段缺失、取值非法一律拒绝。  
  若沿用「复选框读不到就当 `false`」的习惯，一次畸形请求就会变成**静默关闭注册**；  
  一个安全相关的开关不该有这种失败方式
- **注册开关缓存在内存里，但不会成为「改了不生效」的黑洞** —— 页面写入立刻回填缓存，  
  后台每 15 秒还会回源一次，吸收本进程之外的改动（多实例部署、直接改数据库）。  
  缓存本身是必要的：`render` 不能依赖数据库，否则 `serverError` 要渲染的 500 页面  
  会在数据库出问题时一起挂掉

### 已知权衡

使用安全问题替代邮件找回，意味着**第一步需要展示问题**，因此无法像邮箱方式那样完全隐藏  
「该账号是否存在」。这是安全性（自助找回的可用性）与隐私性之间的取舍，系统通过以下方式降低风险：

- 对同一标识的查询与答题做频率限制（默认 15 分钟内 5 次）
- 账号被禁用时与不存在时返回相同的提示
- 答案永不回显，问题文本不做模糊匹配

如果对账号枚举风险更敏感，可以关闭自助注册（`INVENTORY_ALLOW_REGISTRATION=false`），  
由管理员统一开设账号。

<https://github.com/user-attachments/assets/1762701e-bd6c-423f-9165-92f579492900>

## 📄 许可证

[MIT](LICENSE)
