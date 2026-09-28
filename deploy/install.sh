#!/bin/sh
#
# 库存管理系统 —— 一键安装脚本
#
# 自动识别 init 系统：
#   - systemd（Debian / Ubuntu / CentOS / Rocky / openSUSE …）
#   - OpenRC （Alpine Linux）
#
# 用法：
#   sudo ./deploy/install.sh
#   sudo ./deploy/install.sh --arch arm64
#   sudo ./deploy/install.sh --binary ./dist/inventory-server-amd64
#   sudo ./deploy/install.sh --tag v1.0.02
#   sudo ./deploy/install.sh --no-start
#   sudo ./deploy/install.sh --no-backup      # 不装每日自动备份
#
# 安装位置：
#   /opt/inventory/bin/inventory-server   可执行文件（服务用户可写，自更新需要）
#   /var/lib/inventory                    数据库
#   /var/backups/inventory                每日自动备份
#   /etc/inventory/inventory.env          systemd 环境变量文件
#   /etc/conf.d/inventory                 OpenRC 环境变量文件
#
# ⚠️ 安装目录必须对服务用户可写：在线更新会先把自己的二进制改名为
#    <路径>.old，再把新版本放到原路径。放进只读目录会导致更新失败。

set -eu

REPO="jinhuaitao/inventory"
ARCH=""
TAG="latest"
BINARY=""
START=1
BACKUP_TIMER=1

INSTALL_ROOT="/opt/inventory"
BIN_DIR="${INSTALL_ROOT}/bin"
BIN_PATH="${BIN_DIR}/inventory-server"
DATA_DIR="/var/lib/inventory"
LOG_DIR="/var/log/inventory"
BACKUP_DIR="/var/backups/inventory"
SERVICE_USER="inventory"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# ---------------------------------------------------------------------------
# 输出
# ---------------------------------------------------------------------------

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33m警告:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m错误:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	# 打印文件开头连续的注释块（跳过 shebang），去掉行首的 "# "
	awk 'NR > 1 { if ($0 !~ /^#/) exit; sub(/^# ?/, ""); print }' "$0"
	exit 0
}

# ---------------------------------------------------------------------------
# 参数
# ---------------------------------------------------------------------------

while [ $# -gt 0 ]; do
	case "$1" in
		--arch) ARCH="${2:-}"; shift 2 ;;
		--tag) TAG="${2:-}"; shift 2 ;;
		--repo) REPO="${2:-}"; shift 2 ;;
		--binary) BINARY="${2:-}"; shift 2 ;;
		--no-start) START=0; shift ;;
		--no-backup) BACKUP_TIMER=0; shift ;;
		-h|--help) usage ;;
		*) die "未知参数: $1（用 --help 查看用法）" ;;
	esac
done

# ---------------------------------------------------------------------------
# 前置检查
# ---------------------------------------------------------------------------

[ "$(id -u)" -eq 0 ] || die "请用 root 运行（sudo $0）"

detect_arch() {
	case "$(uname -m)" in
		x86_64 | amd64) echo amd64 ;;
		aarch64 | arm64) echo arm64 ;;
		*) die "不支持的 CPU 架构：$(uname -m)（目前只发布 linux/amd64 与 linux/arm64）" ;;
	esac
}

detect_init() {
	if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
		echo systemd
	elif command -v rc-service >/dev/null 2>&1; then
		echo openrc
	else
		die "无法识别 init 系统：既没有运行中的 systemd，也没有 OpenRC。"
	fi
}

[ -n "$ARCH" ] || ARCH="$(detect_arch)"
INIT="$(detect_init)"

info "CPU 架构: linux-${ARCH}"
info "初始化系统: ${INIT}"

# ---------------------------------------------------------------------------
# 服务账号
# ---------------------------------------------------------------------------

ensure_user() {
	if id "$SERVICE_USER" >/dev/null 2>&1; then
		info "服务账号 ${SERVICE_USER} 已存在"
		return 0
	fi

	info "创建服务账号 ${SERVICE_USER}"
	if command -v adduser >/dev/null 2>&1; then
		# Debian 系 adduser 支持 --system/--group；BusyBox adduser 不支持，
		# 因此先按 Debian 语法尝试，失败再按 BusyBox 语法重试。
		if adduser --system --group --home "$INSTALL_ROOT" --no-create-home \
			--shell /usr/sbin/nologin "$SERVICE_USER" >/dev/null 2>&1; then
			return 0
		fi
		adduser -S -D -H -h "$INSTALL_ROOT" -s /sbin/nologin "$SERVICE_USER" >/dev/null 2>&1 \
			|| die "创建服务账号失败"
		return 0
	fi

	useradd --system --home-dir "$INSTALL_ROOT" --shell /usr/sbin/nologin "$SERVICE_USER" \
		|| die "创建服务账号失败"
}

ensure_user

# ---------------------------------------------------------------------------
# 目录
# ---------------------------------------------------------------------------

info "创建目录"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0755 "$INSTALL_ROOT" "$BIN_DIR"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$DATA_DIR"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$BACKUP_DIR"

# ---------------------------------------------------------------------------
# 获取二进制
# ---------------------------------------------------------------------------

fetch() {
	url="$1"
	dest="$2"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 --connect-timeout 15 -o "$dest" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$dest" "$url"
	else
		die "需要 curl 或 wget 才能下载安装包，请先安装其中之一"
	fi
}

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

ASSET_NAME="inventory-server-${ARCH}"

if [ -n "$BINARY" ]; then
	[ -f "$BINARY" ] || die "找不到指定的二进制文件：$BINARY"
	info "使用本地二进制：$BINARY"
	cp "$BINARY" "$TMP_DIR/$ASSET_NAME"
else
	if [ "$TAG" = "latest" ]; then
		BASE_URL="https://github.com/${REPO}/releases/latest/download"
	else
		BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
	fi

	info "下载 ${ASSET_NAME}（${TAG}）"
	fetch "${BASE_URL}/${ASSET_NAME}" "$TMP_DIR/$ASSET_NAME" \
		|| die "下载失败：${BASE_URL}/${ASSET_NAME}
请确认该版本已发布，且包含 linux-${ARCH} 产物。"

	# 校验和：能拿到就校验，拿不到只提示不阻断（与程序内更新逻辑一致）
	if command -v sha256sum >/dev/null 2>&1; then
		if fetch "${BASE_URL}/checksums.txt" "$TMP_DIR/checksums.txt" 2>/dev/null; then
			EXPECTED="$(grep " ${ASSET_NAME}\$" "$TMP_DIR/checksums.txt" 2>/dev/null | head -n1 | cut -d' ' -f1 || true)"
			if [ -n "$EXPECTED" ]; then
				ACTUAL="$(sha256sum "$TMP_DIR/$ASSET_NAME" | cut -d' ' -f1)"
				[ "$EXPECTED" = "$ACTUAL" ] \
					|| die "校验和不匹配！
  期望: ${EXPECTED}
  实际: ${ACTUAL}
请勿使用该文件，可能是下载被篡改或中断。"
				info "SHA-256 校验通过"
			else
				warn "checksums.txt 中没有 ${ASSET_NAME} 的记录，已跳过校验"
			fi
		else
			warn "未找到 checksums.txt，已跳过校验"
		fi
	else
		warn "系统没有 sha256sum，已跳过校验"
	fi
fi

# ELF 魔数检查：防止把 HTML 错误页或空文件当成可执行文件装上去
MAGIC="$(od -An -tx1 -N4 "$TMP_DIR/$ASSET_NAME" 2>/dev/null | tr -d ' \n' || true)"
[ "$MAGIC" = "7f454c46" ] \
	|| die "下载的文件不是有效的 Linux ELF 可执行文件（魔数 ${MAGIC:-空}）"

# ---------------------------------------------------------------------------
# 安装二进制
# ---------------------------------------------------------------------------

# 备份现有二进制，便于回滚
if [ -f "$BIN_PATH" ]; then
	info "备份现有二进制到 ${BIN_PATH}.bak"
	cp -p "$BIN_PATH" "${BIN_PATH}.bak"
fi

info "安装到 ${BIN_PATH}"
install -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0755 "$TMP_DIR/$ASSET_NAME" "$BIN_PATH"

# ---------------------------------------------------------------------------
# 生成会话密钥
# ---------------------------------------------------------------------------

random_secret() {
	if command -v openssl >/dev/null 2>&1; then
		openssl rand -hex 32
	elif [ -r /dev/urandom ]; then
		od -An -tx1 -N32 /dev/urandom | tr -d ' \n'
	elif command -v sha256sum >/dev/null 2>&1; then
		{ date +%s%N; cat /proc/sys/kernel/random/uuid 2>/dev/null || true; } | sha256sum | cut -d' ' -f1
	else
		die "无法生成随机密钥，请安装 openssl 后重试"
	fi
}

# ---------------------------------------------------------------------------
# 安装服务
# ---------------------------------------------------------------------------

if [ "$INIT" = "systemd" ]; then
	info "安装 systemd 单元"
	install -d -m 0750 /etc/inventory
	install -m 0644 "$SCRIPT_DIR/systemd/inventory.service" /etc/systemd/system/inventory.service

	if [ -f /etc/inventory/inventory.env ]; then
		info "保留已有配置 /etc/inventory/inventory.env"
	else
		info "生成配置 /etc/inventory/inventory.env（已写入随机会话密钥）"
		SECRET="$(random_secret)"
		sed "s|^INVENTORY_SESSION_SECRET=.*|INVENTORY_SESSION_SECRET=${SECRET}|" \
			"$SCRIPT_DIR/inventory.env.example" > /etc/inventory/inventory.env
		chmod 0600 /etc/inventory/inventory.env
	fi

	if [ "$BACKUP_TIMER" -eq 1 ]; then
		info "安装每日自动备份（systemd timer）"
		install -m 0644 "$SCRIPT_DIR/systemd/inventory-backup.service" /etc/systemd/system/inventory-backup.service
		install -m 0644 "$SCRIPT_DIR/systemd/inventory-backup.timer" /etc/systemd/system/inventory-backup.timer
	fi

	systemctl daemon-reload
	systemctl enable inventory >/dev/null 2>&1 || true
	if [ "$BACKUP_TIMER" -eq 1 ]; then
		systemctl enable --now inventory-backup.timer >/dev/null 2>&1 || true
	fi
	if [ "$START" -eq 1 ]; then
		systemctl restart inventory
		sleep 1
		systemctl --no-pager --lines=10 status inventory || true
	fi
else
	info "安装 OpenRC 服务"
	install -d -m 0755 /etc/conf.d
	install -m 0755 "$SCRIPT_DIR/openrc/inventory" /etc/init.d/inventory

	# 日志目录必须先建好：supervise-daemon 启动时会直接向该文件写日志
	install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$LOG_DIR"

	if [ -f /etc/conf.d/inventory ]; then
		info "保留已有配置 /etc/conf.d/inventory"
	else
		info "生成配置 /etc/conf.d/inventory（已写入随机会话密钥）"
		SECRET="$(random_secret)"
		sed "s|^INVENTORY_SESSION_SECRET=.*|INVENTORY_SESSION_SECRET=\"${SECRET}\"|" \
			"$SCRIPT_DIR/openrc/inventory.confd" > /etc/conf.d/inventory
		chmod 0600 /etc/conf.d/inventory
	fi

	if [ "$BACKUP_TIMER" -eq 1 ]; then
		info "安装每日自动备份（/etc/periodic/daily）"
		install -d -m 0755 /etc/periodic/daily
		install -m 0755 "$SCRIPT_DIR/openrc/periodic-daily-backup" /etc/periodic/daily/inventory-backup
		rc-update add crond default >/dev/null 2>&1 || true
		rc-service crond start >/dev/null 2>&1 || true
	fi

	rc-update add inventory default >/dev/null 2>&1 || true
	if [ "$START" -eq 1 ]; then
		rc-service inventory restart
		sleep 1
		rc-service inventory status || true
	fi
fi

# ---------------------------------------------------------------------------
# 收尾
# ---------------------------------------------------------------------------

PORT="$(printf '%s' "${INVENTORY_ADDR:-:8080}" | sed 's/.*://')"

if [ "$INIT" = "systemd" ]; then
	CONF_FILE="/etc/inventory/inventory.env"
	OPS="  查看状态     systemctl status inventory
  查看日志     journalctl -u inventory -f
  重启服务     systemctl restart inventory"
else
	CONF_FILE="/etc/conf.d/inventory"
	OPS="  查看状态     rc-service inventory status
  查看日志     tail -f ${LOG_DIR}/inventory.log
  重启服务     rc-service inventory restart"
fi

cat <<EOF

安装完成。

  可执行文件   ${BIN_PATH}
  数据库       ${DATA_DIR}/inventory.db
  每日备份     ${BACKUP_DIR}
  监听地址     :${PORT}
  配置文件     ${CONF_FILE}

常用操作：
${OPS}

首次登录请使用配置中的管理员账号，并立即修改默认密码。

若数据库里已存在演示数据（分类 / 供应商 / 商品），可先预览再清理：
  ${BIN_PATH} --purge-demo-data --dry-run
  ${BIN_PATH} --purge-demo-data

在线更新：登录后台 →「系统更新」→ 一键升级。更新采用就地替换进程映像
（PID 不变），无需手动重启服务；旧版本会保留为 ${BIN_PATH}.old 以便回滚。

备份与换机器：每天 03:30 会自动备份到 ${BACKUP_DIR}，保留最近 14 份。
手动备份随时可执行（**不需要停服务**）：
  ${BIN_PATH} --backup-db ${BACKUP_DIR}
换机器时把备份文件拷到新机器，按 deploy/README.md 的
「数据库备份与迁移」一节恢复即可。
EOF
