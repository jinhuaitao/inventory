# 部署指南（Debian / Alpine）

本目录提供 Linux 服务器上的标准部署方式，同时覆盖两类 init 系统：

| 系统 | init | 服务定义 |
| --- | --- | --- |
| Debian 10+ / Ubuntu 18.04+ / CentOS / Rocky | systemd | `systemd/inventory.service` |
| Alpine Linux 3.18+ | OpenRC | `openrc/inventory` + `openrc/inventory.confd` |

两种方式都用同一个**静态二进制**（纯 Go，无 CGO），因此在 glibc 与 musl 上行为一致。

---

## 1. 目录结构

```
deploy/
├── install.sh                  一键安装（自动识别 systemd / OpenRC）
├── inventory.env.example       systemd 环境变量模板
├── systemd/
│   ├── inventory.service       systemd 单元
│   ├── inventory-backup.service  每日备份单元
│   └── inventory-backup.timer    每日备份定时器
├── openrc/
│   ├── inventory               OpenRC 服务脚本
│   ├── inventory.confd         OpenRC 环境变量模板
│   └── periodic-daily-backup   每日备份（放入 /etc/periodic/daily）
└── README.md                   完整部署文档
```

安装后的文件布局：

```
/opt/inventory/bin/inventory-server      可执行文件（属主 inventory，必须可写）
/opt/inventory/bin/inventory-server.old  更新后留下的回滚点
/var/lib/inventory/inventory.db          数据库（含 -wal / -shm）
/var/backups/inventory/                  每日自动备份，保留最近 14 份
/etc/inventory/inventory.env             systemd 环境变量（0600）
/etc/conf.d/inventory                    OpenRC 环境变量（0600）
/var/log/inventory/inventory.log         OpenRC 日志
```

---

## 2. 快速开始

### Debian / Ubuntu

```sh
sudo ./deploy/install.sh

# 或指定架构 / 本地已编译的二进制
sudo ./deploy/install.sh --arch arm64
sudo ./deploy/install.sh --binary ./dist/inventory-server-amd64

systemctl status inventory
journalctl -u inventory -f
```

### Alpine Linux

```sh
# Alpine 默认没有 curl，脚本会自动回退到 busybox wget
sudo ./deploy/install.sh

rc-service inventory status
tail -f /var/log/inventory/inventory.log
```

> Alpine 上如果系统时间不准，HTTPS 会失败。先执行 `apk add --no-cache ca-certificates`，
> 必要时安装 `chrony` 校时。

### 手动安装（不使用脚本）

systemd：

```sh
install -d -o inventory -g inventory -m 0755 /opt/inventory/bin
install -d -o inventory -g inventory -m 0750 /var/lib/inventory /var/backups/inventory
install -o inventory -g inventory -m 0755 ./inventory-server-amd64 /opt/inventory/bin/inventory-server

install -d -m 0750 /etc/inventory
install -m 0644 deploy/systemd/inventory.service /etc/systemd/system/
install -m 0644 deploy/systemd/inventory-backup.service deploy/systemd/inventory-backup.timer /etc/systemd/system/
install -m 0600 deploy/inventory.env.example /etc/inventory/inventory.env
# 编辑 /etc/inventory/inventory.env，填入 INVENTORY_SESSION_SECRET

systemctl daemon-reload
systemctl enable --now inventory
systemctl enable --now inventory-backup.timer
```

OpenRC：

```sh
install -d -o inventory -g inventory -m 0755 /opt/inventory/bin
install -d -o inventory -g inventory -m 0750 /var/lib/inventory /var/log/inventory /var/backups/inventory
install -o inventory -g inventory -m 0755 ./inventory-server-arm64 /opt/inventory/bin/inventory-server

install -m 0755 deploy/openrc/inventory /etc/init.d/inventory
install -m 0600 deploy/openrc/inventory.confd /etc/conf.d/inventory
install -m 0755 deploy/openrc/periodic-daily-backup /etc/periodic/daily/inventory-backup
# 编辑 /etc/conf.d/inventory，填入 INVENTORY_SESSION_SECRET

rc-update add inventory default
rc-update add crond default
rc-service inventory start
rc-service crond start
```

---

## 3. 权限：为什么安装目录必须可写

在线更新是**自更新**：程序先把自己的二进制改名为 `<路径>.old`，再把新版本放到原路径，
最后用 `syscall.Exec` 替换进程映像。整个过程需要：

1. **对安装目录有写权限** —— 要创建 `.inventory-server.new`、要 rename。
2. **不要放在只读位置** —— 例如 `/usr/local/bin`。systemd 单元里的
   `ProtectSystem=strict` 会让整个文件系统只读，因此 `/opt/inventory` 必须列在
   `ReadWritePaths=` 中（本仓库提供的单元已经配好）。

如果更新时报错「备份当前版本失败（请确认对 ... 有写权限）」，就是这里没配好。

因此 `install.sh` 把二进制放在 `/opt/inventory/bin/` 并把属主设为 `inventory`，
而不是 `/usr/local/bin`。

### 备份目录同样必须可写

网页上的 **数据维护** 页面（见 §6.8）要在 `/var/backups/inventory` 里创建与删除备份，
因此该目录也必须列进 `ReadWritePaths`。本仓库的 systemd 单元已经写好了两个路径：

```ini
ReadWritePaths=/opt/inventory /var/backups/inventory
```

少了第二个路径时，页面会报「创建备份失败: ... read-only file system」。
目录属主与权限也要对：

```sh
sudo install -d -o inventory -g inventory -m 0750 /var/backups/inventory
```

> 如果你坚持换目录，请同时修改服务定义里的 `ExecStart`（systemd）/`command`（OpenRC）
> 以及 `ReadWritePaths`，并确保服务用户对该目录可写。换备份目录还要同步改
> `INVENTORY_BACKUP_DIR`，否则网页与每日定时任务会各写各的。

---

## 4. 更新后的自动重启

点「立即更新」之后**不需要**手动 `systemctl restart`：

- 程序用 `syscall.Exec` 就地替换进程映像，**PID 保持不变**；
- systemd 继续跟踪同一个 PID，不会认为服务退出，也不会产生第二个进程；
- OpenRC 的 `supervise-daemon` 同理，不会误判为「服务崩溃」而重启；
- 监听端口在 exec 时由内核释放并重新绑定，无需外部进程管理器配合。

服务定义里只保留 `Restart=on-failure`（systemd）与 `respawn_max=0`（OpenRC）作为
真正异常退出时的兜底。

### 回滚

更新成功后旧版本保留在 `/opt/inventory/bin/inventory-server.old`。

```sh
# systemd
sudo systemctl stop inventory
sudo cp -p /opt/inventory/bin/inventory-server.old /opt/inventory/bin/inventory-server
sudo systemctl start inventory

# OpenRC
sudo rc-service inventory stop
sudo cp -p /opt/inventory/bin/inventory-server.old /opt/inventory/bin/inventory-server
sudo rc-service inventory start
```

`install.sh` 还会在覆盖前额外留一份 `/opt/inventory/bin/inventory-server.bak`。

---

## 5. 清理演示数据

首次启动时如果数据库为空，程序会写入一组演示数据（4 个分类、3 个供应商、12 个商品）。
生产环境默认**不会**写入（`INVENTORY_SEED_DEMO_DATA` 默认随环境：production 关闭）。
但如果之前用开发模式跑过，演示数据会留在库里，需要显式清理。

**方式一：网页界面（推荐，不用 SSH）**

用管理员账号登录 → 侧边栏 **数据维护** → 「清理演示数据」卡片会先显示将删除的条数，
确认后点执行即可。执行前系统会**自动做一次安全备份**，备份名会出现在同一页的备份列表里。

**方式二：命令行**

```sh
# 先预览将删除的内容
sudo -u inventory /opt/inventory/bin/inventory-server --purge-demo-data --dry-run

# 确认无误后执行
sudo -u inventory /opt/inventory/bin/inventory-server --purge-demo-data
```

清理规则（宁可漏删也不误删）：

- 商品：只删 `is_demo=1` 的行；
- 库存流水：只删除挂在上述商品下的记录；
- 分类 / 供应商：命中演示清单**且已无商品引用**才会删除，仍被引用的会在输出里列出来。

**旧版本数据库（加 `is_demo` 标记之前写入的）**：那些演示商品没有标记，
命令会把它们**列出来但不删除**，并提示加 `--purge-legacy-demo` 重跑：

```sh
# 确认列出的 SKU 确实是旧版遗留的演示数据后
sudo -u inventory /opt/inventory/bin/inventory-server \
    --purge-demo-data --purge-legacy-demo
```

之所以不默认按 SKU 删除：SKU 是用户可以自由填写的业务字段，用户完全可能
在自己的商品上用 `SKU-1001` 这样的编号。无条件按 SKU 匹配会连带删掉用户自建的
商品**及其全部库存流水**，而且不可逆。所以默认只认标记，SKU 命中的交给人工判断。

管理员账号不会被删除。命令是幂等的，重复执行会提示「未发现内置演示数据」。

> **清理前先备份。** 清理不可回滚，务必留一个还原点。
> 用 `--backup-db` 而不是 `cp` —— 它不用停服务，而且会当场回读校验、
> 打印各表行数，确保拿到的是能用的文件（原因见 §6.1）：
>
> ```sh
> sudo -u inventory /opt/inventory/bin/inventory-server \
>     --backup-db /tmp/清理演示数据前.db --force
> ```
>
> 想回滚就按 §6.5 把该文件放回 `/var/lib/inventory/inventory.db`。

---

## 6. 数据库备份与迁移（换机器看这里）

### 6.1 为什么不能直接 `cp inventory.db`

SQLite 开启了 **WAL 模式**（`journal_mode(WAL)`）：新提交的事务先写 `inventory.db-wal`，
之后某个时刻才合并（checkpoint）进 `inventory.db`。**在合并之前，主文件里可能什么都没有。**

实测（服务刚跑完首次初始化，写入 4 分类 / 3 供应商 / 12 商品）：

```
运行中的 data/ 目录：
  inventory.db        4,096 字节      ← 几乎是个空壳
  inventory.db-wal  416,152 字节      ← 数据全在这里
  inventory.db-shm   32,768 字节

此时 cp inventory.db 后打开：no such table: products   ← 完全读不出数据
```

所以「直接拷贝 `inventory.db`」不是"可能少几条"，而是**可能拷到一个空库**。

| 做法 | 要停服吗 | 可靠性 |
| --- | --- | --- |
| `--backup-db`（内部用 `VACUUM INTO`） | **不用** | ✅ 推荐 |
| 停服后 `cp inventory.db` | 要 | ✅ 可以（须先确认 `-wal` 已消失） |
| 运行中只 `cp inventory.db` | 不用 | ❌ 可能拷到空库 |
| 运行中把 `.db` + `-wal` + `-shm` 一起拷 | 不用 | ⚠️ 不可靠，拷贝过程中可能正好在写入 |

> 停服后 WAL 会被自动合并并删除。实测：正常收到 SIGTERM 退出后，
> `inventory.db` 从 4 KB 变成 159 KB，`-wal` 消失，此时 `cp` 是安全的。
> **但被 `kill -9` 或断电打断时 WAL 会残留**，这时直接 `cp` 依然会丢数据。

### 6.2 手动备份（不用停服务）

```sh
# 备份到目录：自动命名 inventory-YYYYMMDD-HHMMSS.db
sudo -u inventory /opt/inventory/bin/inventory-server --backup-db /var/backups/inventory

# 备份到指定文件
sudo -u inventory /opt/inventory/bin/inventory-server --backup-db /tmp/换机器前.db

# 目录 + 只保留最近 7 份
sudo -u inventory /opt/inventory/bin/inventory-server --backup-db /var/backups/inventory --keep 7

# 目标已存在时加 --force 覆盖
sudo -u inventory /opt/inventory/bin/inventory-server --backup-db /tmp/manual.db --force
```

输出会把备份文件的**完整性检查结果与各表行数**一并打印出来，确认拿到的是能用的文件：

```
源数据库: /var/lib/inventory/inventory.db
备份文件: /var/backups/inventory/inventory-20260928-115518.db
文件大小: 0.15 MB
完整性检查: ok
记录数:
  users            1
  categories       4
  suppliers        3
  products         12
  stock_movements  11
```

参数说明：

| 参数 | 作用 |
| --- | --- |
| `--backup-db <路径>` | 备份目标。**不带扩展名**或以 `/` 结尾视为目录，自动生成带时间戳的文件名；带扩展名视为文件 |
| `--force` | 目标文件已存在时覆盖 |
| `--keep N` | 目标是目录时，只保留最新 N 份自动命名的备份（`0` 表示不清理） |

> `--keep` 只清理严格匹配 `inventory-YYYYMMDD-HHMMSS.db` 的文件，
> 手工放进去的备份或改过名的文件不会被删。

### 6.3 自动备份（`install.sh` 已装好）

安装脚本默认就会配好每日备份，备份到 `/var/backups/inventory`，保留最近 14 份。

**Debian / Ubuntu（systemd timer）**

```sh
systemctl list-timers inventory-backup.timer   # 看下次执行时间
systemctl start inventory-backup.service       # 立刻跑一次
journalctl -u inventory-backup.service         # 看备份日志
```

计划时间 `03:30`，`Persistent=true` 表示错过的计划会在开机后补跑。
不想要自动备份就用 `--no-backup` 重装，或 `systemctl disable --now inventory-backup.timer`。

**Alpine（BusyBox crond）**

```sh
/etc/periodic/daily/inventory-backup    # 手动跑一次
rc-service crond status                 # 确认 crond 在运行
```

脚本位于 `/etc/periodic/daily/inventory-backup`，由 `crond` 每日执行。

### 6.4 换机器：完整步骤

**旧机器上**

```sh
# 1) 备份（不用停服务）
sudo -u inventory /opt/inventory/bin/inventory-server --backup-db /tmp/迁移.db

# 2) 记下会话密钥，这样迁移后所有人的登录状态不用重来
sudo grep INVENTORY_SESSION_SECRET /etc/inventory/inventory.env    # Alpine: /etc/conf.d/inventory

# 3) 把备份文件和密钥拷到新机器（scp / U 盘 / 网盘都行）
scp /tmp/迁移.db  新机器:/tmp/
```

**新机器上**

```sh
# 1) 装好程序（会自动建账号、目录、服务与配置）
sudo ./deploy/install.sh --no-start

# 2) 停服务（此时数据库还是空的，先停掉再放文件）
sudo systemctl stop inventory          # Alpine: rc-service inventory stop

# 3) 用备份替换数据库；顺手删掉可能存在的 WAL 残留
sudo rm -f /var/lib/inventory/inventory.db-wal /var/lib/inventory/inventory.db-shm
sudo cp /tmp/迁移.db /var/lib/inventory/inventory.db
sudo chown inventory:inventory /var/lib/inventory/inventory.db
sudo chmod 0640 /var/lib/inventory/inventory.db

# 4) 对齐会话密钥（可选，但建议做，否则所有人需重新登录）
sudo vi /etc/inventory/inventory.env   # 填入旧机器的 INVENTORY_SESSION_SECRET

# 5) 起服务
sudo systemctl start inventory
sudo systemctl status inventory
```

**验证迁移结果**

```sh
# 看日志有没有报错
journalctl -u inventory -n 30

# 数据库行数应与旧机器一致
sudo -u inventory /opt/inventory/bin/inventory-server --backup-db /tmp/verify.db
# 输出里的「记录数」就是当前库的真实行数
```

最后用原账号登录一次确认。账号密码是 bcrypt 摘要存在库里的，**跟着数据库一起迁移**，
不需要重新创建用户。

> ⚠️ 第 3 步的 `chown` 不能省。备份文件是旧机器上的属主，直接拷过来新机器的
> `inventory` 用户可能没有写权限，服务会报 `attempt to write a readonly database`。

### 6.5 从备份恢复（同一台机器）

```sh
sudo systemctl stop inventory
sudo rm -f /var/lib/inventory/inventory.db-wal /var/lib/inventory/inventory.db-shm
sudo cp /var/backups/inventory/inventory-20260928-033000.db /var/lib/inventory/inventory.db
sudo chown inventory:inventory /var/lib/inventory/inventory.db
sudo systemctl start inventory
```

想更保险，先把当前库挪开而不是覆盖：

```sh
sudo mv /var/lib/inventory/inventory.db /var/lib/inventory/inventory.db.broken
```

### 6.6 备份放哪儿

`/var/backups/inventory` 与数据库在同一块盘上 —— **盘坏了一起没**。
生产环境建议再做一层异地：

```sh
# 每天把最新备份同步到别的机器
rsync -a --delete /var/backups/inventory/ backup-host:/srv/inventory-backups/
```

```sh
# 或者推到对象存储（以腾讯云 COS 为例，需先装 coscmd）
coscmd upload -r /var/backups/inventory/ /inventory-backups/
```

定时任务里加一行即可（systemd 用 `OnCalendar` 另建一个 timer，Alpine 放进
`/etc/periodic/daily/`）。

### 6.7 需要一起迁移的东西

| 内容 | 位置 | 是否必须 |
| --- | --- | --- |
| 数据库 | `/var/lib/inventory/inventory.db` | **必须**（用 6.2 的备份文件，别直接拷运行中的 .db） |
| 会话密钥 | `/etc/inventory/inventory.env` 或 `/etc/conf.d/inventory` | 建议（否则所有人重新登录） |
| 程序本体 | `/opt/inventory/bin/inventory-server` | 不必，新机器 `install.sh` 重新下载 |
| 演示数据 | 在数据库里 | 不必，新机器可用 `--purge-demo-data` 清掉 |
| 服务定义 | `deploy/` | 不必，`install.sh` 会装 |

### 6.8 用网页界面备份 / 恢复（不用 SSH）

上面几节都是命令行操作。管理员登录后，侧边栏 **数据维护** 页面（紧邻「系统更新」）
提供同一套能力的网页版本，适合不方便登录服务器时使用：

| 能力 | 说明 |
| --- | --- |
| 立即备份 | 调 `VACUUM INTO`，**不用停服务**；产出已合并 WAL 的单文件 |
| 下载备份 | 直接下到本地浏览器，方便做异地留存（见 §6.6） |
| 删除备份 | 清理不再需要的备份文件 |
| 上传恢复 | 上传 `.db`，校验通过后暂存，服务重启时换入 |
| 清理演示数据 | 先预览条数，确认后执行，并自动先做一次安全备份 |

**恢复为什么需要重启。** 运行中的数据库连接池持有 `inventory.db`，就地覆盖会让连接
指向已被替换的 inode。所以流程拆成两步：

1. 上传的文件先做完整性检查（`integrity_check` + 各表行数），不通过直接拒绝、不落盘；
2. 通过后暂存为 `inventory.db.restore-pending`，页面提示稍后自动重启；
3. 服务重启时，在 `database.Open()` **之前** 完成换库：
   - 当前库改名为 `inventory.db.before-restore-<时间戳>`（回滚点）
   - 删掉残留的 `inventory.db-wal` / `-shm`（**关键**：旧 WAL 套到新库上会直接把库写坏）
   - 把暂存文件换入

> ⚠️ **残留的 `-wal` / `-shm` 必须清掉。** 这正是 §6.4 第 3 步手工 `rm -f` 的原因，
> 网页恢复会自动处理，不用你操心。

**回滚**：确认数据没问题后可以删掉归档；要退回上一版就把
`inventory.db.before-restore-<时间戳>` 改名回 `inventory.db`（同样记得先删 `-wal` / `-shm`），
再重启服务。

**上传失败会怎样**：文件有问题的，重启时会被改名为 `inventory.db.restore-failed` 保留证据，
**原库不受影响**，服务照常启动，日志里会有 `WARN` 记录。

**体积上限**：单次上传不超过 512 MB（`middleware.MaxUploadBytes`）。

---

## 7. 环境变量

完整清单见 `inventory.env.example`（systemd）与 `openrc/inventory.confd`（OpenRC）。
几个必须注意的：

| 变量 | 说明 |
| --- | --- |
| `INVENTORY_ENV` | `production` 时启用 JSON 日志、要求显式会话密钥、默认不写入演示数据 |
| `INVENTORY_SESSION_SECRET` | **生产环境必填**，至少 16 字符。`openssl rand -hex 32` |
| `INVENTORY_DATA_DIR` | 数据库目录，默认 `/var/lib/inventory` |
| `INVENTORY_ADDR` | 监听地址，默认 `:8080` |
| `INVENTORY_SEED_DEMO_DATA` | 是否写入演示数据；不设置时 production 为 false、其他为 true |
| `INVENTORY_TRUSTED_PROXIES` | 允许采信 `X-Forwarded-For` / `X-Real-IP` 的网段，默认 `127.0.0.1/8,::1/128`。代理在别的机器上时必须显式配置，**不要**填 `0.0.0.0/0` |
| `INVENTORY_ALLOW_REGISTRATION` | 自助注册的**初始**开关。管理员可在「用户管理」页随时覆盖它，**页面设置优先于本变量**，改完不必重启；页面上还有「恢复为环境变量默认值」把控制权交还到这里 |
| `INVENTORY_DEFAULT_ROLE` | 自助注册者的默认角色，可选 `viewer` / `manager`。**填 `admin` 会让程序拒绝启动**（等于任何人自助获得管理员） |
| `INVENTORY_MAX_LOGIN_ATTEMPTS` | 同一「账号 + 来源 IP」的失败上限，默认 5。同一账号跨来源的兜底上限是它的 10 倍 |
| `INVENTORY_LOCKOUT_WINDOW` | 上述失败计数的统计窗口，默认 `15m` |
| `INVENTORY_BACKUP_DIR` | 备份目录，默认 `/var/backups/inventory`；网页「数据维护」与每日定时任务共用 |
| `INVENTORY_BACKUP_KEEP` | 自动命名备份的保留份数，默认 14；`0` 表示不自动清理 |
| `INVENTORY_UPDATE_ENABLED` | 是否启用在线上更新；官方构建已注入仓库地址 |
| `INVENTORY_ADMIN_PASSWORD` | 留空则使用默认密码 `Admin@12345`，登录后立即修改 |

修改配置后重启服务生效：

```sh
sudo systemctl restart inventory      # Debian
sudo rc-service inventory restart     # Alpine
```

---

## 8. 反向代理（可选）

程序自身只监听 HTTP。要对外提供 HTTPS，建议在前面加一层 Nginx：

```nginx
server {
    listen 443 ssl http2;
    server_name inventory.example.com;

    ssl_certificate     /etc/letsencrypt/live/inventory.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/inventory.example.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 90s;
    }
}
```

同时在环境变量里设置 `INVENTORY_BASE_URL=https://inventory.example.com`。

### 关于来源 IP

程序只有在请求**确实来自可信代理**时才会采信 `X-Forwarded-For` / `X-Real-IP`。
默认只信任回环地址（`127.0.0.1/8,::1/128`），正好覆盖上面「Nginx 与程序同机」的写法，
因此这个配置通常不用改。

- 代理在**别的机器或容器**里时，必须把它的网段加进去，否则日志与审计里记录的
  会是代理自己的地址：
  ```sh
  INVENTORY_TRUSTED_PROXIES=127.0.0.1/8,::1/128,10.0.0.0/8
  ```
- **不要**配成 `0.0.0.0/0`。那等于任何人都能用一个请求头把自己的来源 IP 改成任意值，
  基于 IP 的限流与审计会全部失效。
- 取值时用的是 `X-Forwarded-For` 里**最右侧的不可信地址**（不是最左侧）。
  Nginx 的 `$proxy_add_x_forwarded_for` 是**追加**，所以最左边那一段是客户端
  自己塞进来的伪造值，取它会踩坑。

---

## 9. 故障排查

| 现象 | 原因与处理 |
| --- | --- |
| 启动即退出，日志提示「生产环境必须设置 INVENTORY_SESSION_SECRET」 | 在环境变量文件里填入 32 字节随机串 |
| 启动即退出，提示「INVENTORY_DEFAULT_ROLE 不能是 admin」 | 自助注册默认给管理员等于人人都是管理员；改成 `viewer` / `manager` |
| 启动即退出，提示「INVENTORY_TRUSTED_PROXIES 解析失败」 | 网段写错了；支持 CIDR 与裸 IP，逗号分隔 |
| 日志里的来源 IP 全是 `127.0.0.1` 或代理地址 | 代理不在默认可信网段内；用 `INVENTORY_TRUSTED_PROXIES` 加上它 |
| 更新报「备份当前版本失败（请确认对 ... 有写权限）」 | 安装目录不可写；检查属主与 `ReadWritePaths` |
| 更新报「无法访问 GitHub」/ 超时 | 服务器无法直连 GitHub；配置 `INVENTORY_UPDATE_TOKEN` 或改用内网发布源 |
| 更新后版本号没变 | 确认是否手动把二进制放进了只读目录；正常情况下进程会以新版本重启 |
| Alpine 下载失败、证书报错 | `apk add --no-cache ca-certificates`，并校准系统时间 |
| 后台有演示数据 | 执行 `--purge-demo-data`（见第 5 节），或到网页「数据维护」页清理 |
| 备份后打开发现没有表 / 数据不全 | 多半是直接 `cp inventory.db` 漏了 WAL，改用 `--backup-db`（见第 6 节） |
| 网页「数据维护」报创建备份失败 / read-only file system | 备份目录不可写；检查 `ReadWritePaths` 是否含 `/var/backups/inventory`（见第 3 节） |
| 网页上传备份后一直提示「等待重启生效」 | 服务没有重启成功；`systemctl status inventory` 看退出原因，日志里搜「应用待恢复的数据库」 |
| 恢复后发现数据没变，但多了 `inventory.db.restore-failed` | 上传的文件没通过完整性检查，原库被保留；换一份备份重试 |
| 上传恢复表单报 403 安全校验未通过 | 页面停留过久导致令牌过期，刷新后重试；若刷新仍报错，说明 CSRF 中间件未能读取 multipart 表单（已修复，见 `internal/middleware`） |
| 迁移后报 `attempt to write a readonly database` | 忘了 `chown inventory:inventory /var/lib/inventory/inventory.db` |
| 登录页不显示「立即注册」入口 | 管理员在「用户管理」页关闭了自助注册。该页面的设置**优先于** `INVENTORY_ALLOW_REGISTRATION`，在那里开启，或点「恢复为环境变量默认值」把控制权交还配置 |
| 改了 `INVENTORY_ALLOW_REGISTRATION` 但注册状态没变 | 同上：页面上已经设置过就会覆盖环境变量。启动日志里有一行「自助注册状态已就绪」，会写明当前取值来自哪一边 |
| 从备份恢复后注册状态变了 | 开关存在数据库里，会跟着备份一起恢复。恢复完到「用户管理」页确认一次即可 |
| 迁移后所有人都要重新登录 | 新机器的 `INVENTORY_SESSION_SECRET` 与旧机器不一致 |
| 自动备份没跑 | systemd：`systemctl list-timers inventory-backup.timer`；Alpine：确认 `crond` 在运行 |
| 端口被占用 | 修改 `INVENTORY_ADDR`，或排查 `ss -lntp | grep 8080` |

查看版本与更新状态：

```sh
/opt/inventory/bin/inventory-server --version
/opt/inventory/bin/inventory-server --check-update
```
