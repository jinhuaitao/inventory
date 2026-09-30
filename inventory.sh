#!/bin/sh
# ==============================================================================
#   Inventory Server · 一键安装与管理脚本
#   ---------------------------------------------------------------------------
#   支持系统 : Debian / Ubuntu (systemd)  ·  Alpine Linux (OpenRC)
#   支持架构 : x86_64 / amd64              ·  aarch64 / arm64
#
#   使用方式 : sh inventory.sh [install|update|restart|status|uninstall|purge]
#              不带参数运行 → 进入交互式管理菜单
# ==============================================================================

set -u

# ══════════════════════════════════════════════════ 可 配 置 区 ══════════════
APP_TITLE="Inventory Server"
APP_TITLE_EN="I N V E N T O R Y   S E R V E R"
APP_DESC="库存管理系统"
SERVICE_NAME="inventory-server"
BIN_PATH="/usr/local/bin/${SERVICE_NAME}"
DATA_DIR="/var/lib/${SERVICE_NAME}"
LOG_FILE="/var/log/${SERVICE_NAME}.log"
PORT="${PORT:-8080}"                        # 访问端口（需与程序实际监听端口一致）
REPO="jinhuaitao/inventory"
BASE_URL="https://github.com/${REPO}/releases/latest/download"
RULE="──────────────────────────────────────────────────────"

# 运行时探测结果（先给默认值，避免 set -u 下未绑定）
INIT="unknown"
ARCH=""
# ═════════════════════════════════════════════════════════════════════════════

# ─────────────────────────────────────────────────────────────────── 颜色 ──
setup_colors() {
    if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
        ESC=$(printf '\033')
        BOLD="${ESC}[1m";  DIM="${ESC}[2m";     RESET="${ESC}[0m"
        RED="${ESC}[31m";  GREEN="${ESC}[32m";  YELLOW="${ESC}[33m"
        BLUE="${ESC}[34m"; CYAN="${ESC}[36m";   GRAY="${ESC}[90m"
    else
        BOLD=''; DIM=''; RESET=''
        RED=''; GREEN=''; YELLOW=''; BLUE=''; CYAN=''; GRAY=''
    fi
}

# ─────────────────────────────────────────────────────────────────── 输出 ──
info() { printf '  %s●%s  %s\n' "$BLUE"   "$RESET" "$*"; }
ok()   { printf '  %s✔%s  %s\n' "$GREEN"  "$RESET" "$*"; }
warn() { printf '  %s▲%s  %s\n' "$YELLOW" "$RESET" "$*"; }
fail() { printf '  %s✖%s  %s\n' "$RED"    "$RESET" "$*" >&2; }
step() { printf '\n  %s▸%s  %s%s%s\n' "$CYAN" "$RESET" "$BOLD" "$*" "$RESET"; }
note() { printf '    %s%s%s\n' "$GRAY" "$*" "$RESET"; }
line() { printf '  %s%s%s\n' "$GRAY" "$RULE" "$RESET"; }

clear_screen() {
    [ -t 1 ] && clear 2>/dev/null
    return 0
}

print_banner() {
    clear_screen
    printf '\n'
    printf '  %s%s%s\n\n' "$GRAY" "$RULE" "$RESET"
    printf '   %s%s%s\n' "$CYAN$BOLD" "$APP_TITLE_EN" "$RESET"
    printf '   %s%s%s\n\n' "$GRAY" "${APP_DESC} · 一键部署与管理" "$RESET"
    printf '  %s%s%s\n' "$GRAY" "$RULE" "$RESET"
}

print_menu() {
    printf '\n'
    printf '  %s┏━ 主 菜 单 %s%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n' "$BOLD" "$CYAN" "$GRAY" "$RESET"
    printf '  %s┃%s\n' "$GRAY" "$RESET"
    printf '  %s┃%s   %s1%s  安装 / 更新      %s安装或升级到最新版本%s\n' "$GRAY" "$RESET" "$GREEN" "$RESET" "$DIM" "$RESET"
    printf '  %s┃%s   %s2%s  重启服务         %s重新启动后台服务%s\n'     "$GRAY" "$RESET" "$GREEN" "$RESET" "$DIM" "$RESET"
    printf '  %s┃%s   %s3%s  查看状态         %s运行状态与访问地址%s\n'     "$GRAY" "$RESET" "$GREEN" "$RESET" "$DIM" "$RESET"
    printf '  %s┃%s   %s4%s  卸载             %s移除服务与程序文件%s\n'     "$GRAY" "$RESET" "$GREEN" "$RESET" "$DIM" "$RESET"
    printf '  %s┃%s\n' "$GRAY" "$RESET"
    printf '  %s┃%s   %s0%s  退出\n' "$GRAY" "$RESET" "$GREEN" "$RESET"
    printf '  %s┃%s\n' "$GRAY" "$RESET"
    printf '  %s┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n' "$GRAY" "$RESET"
}

pause() {
    [ -t 0 ] || return 0
    printf '\n  %s按回车键返回主菜单...%s' "$GRAY" "$RESET"
    read -r _ || true
}

usage() {
    printf '\n  用法: sh %s [命令]\n\n' "$0"
    printf '  命令:\n'
    printf '    install      安装 / 更新 %s\n' "$APP_TITLE"
    printf '    restart      重启服务\n'
    printf '    status       查看运行状态\n'
    printf '    uninstall    卸载（保留数据目录）\n'
    printf '    purge        卸载并清除数据与日志\n'
    printf '    menu         进入交互式菜单（默认）\n\n'
    printf '  环境变量:\n'
    printf '    PORT=8080            自定义展示端口\n'
    printf '    ARCH_OVERRIDE=amd64  手动指定下载架构\n'
    printf '    NO_COLOR=1           关闭彩色输出\n\n'
}

# ─────────────────────────────────────────────────────────────── 环境探测 ──
require_root() {
    if [ "$(id -u)" != "0" ]; then
        fail "需要 root 权限，请使用 root 用户运行，或加 sudo 前缀。"
        exit 1
    fi
}

detect_init() {
    if [ -f /etc/alpine-release ]; then
        INIT="openrc"
    elif command -v systemctl >/dev/null 2>&1; then
        INIT="systemd"
    else
        INIT="unknown"
    fi
}

detect_arch() {
    if [ -n "${ARCH_OVERRIDE:-}" ]; then
        ARCH="$ARCH_OVERRIDE"
        ok "使用手动指定的架构 ${BOLD}${ARCH}${RESET}"
        return 0
    fi
    case "$(uname -m)" in
        x86_64|amd64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)
            fail "暂不支持的系统架构: $(uname -m)"
            note "如该架构已有构建产物，可手动指定："
            note "  ARCH_OVERRIDE=<名称> sh $0 install"
            return 1
            ;;
    esac
    ok "系统架构 ${BOLD}$(uname -m)${RESET} → ${BOLD}${ARCH}${RESET}"
}

ensure_curl() {
    command -v curl >/dev/null 2>&1 && return 0
    info "未检测到 curl，正在自动安装..."
    if [ -f /etc/alpine-release ]; then
        apk add --no-cache curl >/dev/null 2>&1
    elif command -v apt-get >/dev/null 2>&1; then
        apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq curl >/dev/null 2>&1
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y curl >/dev/null 2>&1
    elif command -v yum >/dev/null 2>&1; then
        yum install -y curl >/dev/null 2>&1
    fi
    if ! command -v curl >/dev/null 2>&1; then
        fail "curl 安装失败，请手动安装后重试。"
        return 1
    fi
    ok "curl 安装完成"
}

ensure_dirs() {
    mkdir -p "$(dirname "$BIN_PATH")" >/dev/null 2>&1
    mkdir -p "$DATA_DIR" >/dev/null 2>&1
    return 0
}

get_ip() {
    _ip=''
    for _u in https://ipv4.icanhazip.com https://ifconfig.me/ip https://api.ipify.org; do
        _ip=$(curl -fsS -4 -m 3 "$_u" 2>/dev/null | tr -d '\r\n ')
        [ -n "$_ip" ] && break
    done
    if [ -z "$_ip" ]; then
        _ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    [ -z "$_ip" ] && _ip='<服务器IP>'
    printf '%s' "$_ip"
}

# ─────────────────────────────────────────────────────────────── 服务控制 ──
service_installed() {
    case "$INIT" in
        systemd) [ -f "/etc/systemd/system/${SERVICE_NAME}.service" ] ;;
        openrc)  [ -f "/etc/init.d/${SERVICE_NAME}" ] ;;
        *)       return 1 ;;
    esac
}

service_running() {
    case "$INIT" in
        systemd) systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null ;;
        openrc)  rc-service "$SERVICE_NAME" status >/dev/null 2>&1 ;;
        *)       return 1 ;;
    esac
}

stop_service() {
    case "$INIT" in
        systemd) systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 ;;
        openrc)  rc-service "$SERVICE_NAME" stop >/dev/null 2>&1 ;;
    esac
    return 0
}

start_service() {
    case "$INIT" in
        systemd) systemctl start "$SERVICE_NAME" >/dev/null 2>&1 ;;
        openrc)  rc-service "$SERVICE_NAME" start >/dev/null 2>&1 ;;
    esac
    return 0
}

enable_service() {
    case "$INIT" in
        systemd)
            systemctl daemon-reload >/dev/null 2>&1
            systemctl enable "$SERVICE_NAME" >/dev/null 2>&1
            ;;
        openrc)
            rc-update add "$SERVICE_NAME" default >/dev/null 2>&1
            ;;
    esac
    return 0
}

status_text() {
    if service_running; then
        printf '%s● 运行中 (Active)%s' "$GREEN" "$RESET"
    else
        printf '%s● 已停止 (Inactive)%s' "$RED" "$RESET"
    fi
}

autostart_text() {
    case "$INIT" in
        systemd)
            if systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
                printf '%s已启用%s' "$GREEN" "$RESET"
            else
                printf '%s未启用%s' "$RED" "$RESET"
            fi
            ;;
        openrc)
            if rc-update show default 2>/dev/null | grep -q "$SERVICE_NAME"; then
                printf '%s已启用%s' "$GREEN" "$RESET"
            else
                printf '%s未启用%s' "$RED" "$RESET"
            fi
            ;;
        *) printf '%s—%s' "$GRAY" "$RESET" ;;
    esac
}

# ─────────────────────────────────────────────────────────────── 服务配置 ──
write_systemd_unit() {
    cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=${APP_TITLE} (${APP_DESC})
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=${DATA_DIR}
ExecStart=${BIN_PATH}
Restart=always
RestartSec=3
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

# ---- 安全加固（若程序需要更高权限，可逐条注释掉）----
NoNewPrivileges=true
PrivateTmp=true
# ProtectSystem=full
# ProtectHome=true

[Install]
WantedBy=multi-user.target
EOF
    ok "已写入 ${BOLD}/etc/systemd/system/${SERVICE_NAME}.service${RESET}"
}

write_openrc_script() {
    cat > "/etc/init.d/${SERVICE_NAME}" <<EOF
#!/sbin/openrc-run
# ${APP_TITLE} - OpenRC service

name="${SERVICE_NAME}"
description="${APP_TITLE} (${APP_DESC})"
command="${BIN_PATH}"
command_background="yes"
directory="${DATA_DIR}"
pidfile="/run/\${RC_SVCNAME}.pid"
output_log="${LOG_FILE}"
error_log="${LOG_FILE}"

depend() {
    need net
    after firewall
    use dns logger
}
EOF
    chmod 0755 "/etc/init.d/${SERVICE_NAME}"
    ok "已写入 ${BOLD}/etc/init.d/${SERVICE_NAME}${RESET}"
}

# ─────────────────────────────────────────────────────────────── 下载安装 ──
download_binary() {
    _url="${BASE_URL}/${SERVICE_NAME}-${ARCH}"
    _tmp="${BIN_PATH}.download.$$"

    info "下载地址 ${DIM}${_url}${RESET}"
    if ! curl -fL --retry 3 --retry-delay 1 --connect-timeout 15 \
              --progress-bar -o "$_tmp" "$_url"; then
        rm -f "$_tmp"
        fail "下载失败：请检查网络连通性，或确认该架构 (${ARCH}) 是否已发布构建产物。"
        return 1
    fi

    # 校验是否为真实可执行文件（防止 404 页面 / 代理拦截页被当成程序安装）
    _magic=$(head -c 4 "$_tmp" 2>/dev/null | od -An -tx1 2>/dev/null | tr -d ' \n\t')
    if [ -n "$_magic" ] && [ "$_magic" != "7f454c46" ]; then
        rm -f "$_tmp"
        fail "下载内容不是有效的可执行文件（可能为 404 页面或网络拦截）。"
        return 1
    fi

    chmod 0755 "$_tmp" || { rm -f "$_tmp"; fail "设置执行权限失败。"; return 1; }
    mv -f "$_tmp" "$BIN_PATH" || { rm -f "$_tmp"; fail "写入 ${BIN_PATH} 失败。"; return 1; }
    ok "已安装至 ${BOLD}${BIN_PATH}${RESET}"
}

print_result() {
    _ip=$(get_ip)
    printf '\n'
    line
    printf '  %s✔%s  %s%s%s\n' "$GREEN" "$RESET" "$BOLD" "$1" "$RESET"
    line
    printf '  %s服务名称%s   %s\n' "$DIM" "$RESET" "$SERVICE_NAME"
    printf '  %s程序路径%s   %s\n' "$DIM" "$RESET" "$BIN_PATH"
    printf '  %s运行状态%s   %s\n' "$DIM" "$RESET" "$(status_text)"
    printf '  %s访问地址%s   %shttp://%s:%s%s\n' "$DIM" "$RESET" "$CYAN" "$_ip" "$PORT" "$RESET"
    line
    printf '\n'
}

# ─────────────────────────────────────────────────────────────────── 功能 ──
do_install() {
    print_banner
    step "安装 ${APP_TITLE}"
    require_root
    ensure_curl || return 1
    detect_arch || return 1
    detect_init
    [ "$INIT" = "unknown" ] && warn "未识别的初始化系统，将只安装二进制文件，不配置开机自启。"
    ensure_dirs

    if service_installed; then
        info "检测到已安装的服务，先停止旧进程以释放文件占用..."
        stop_service
        sleep 1
    fi

    step "下载程序"
    download_binary || return 1

    if [ "$INIT" != "unknown" ]; then
        step "配置系统服务 (${INIT})"
        if [ "$INIT" = "systemd" ]; then
            write_systemd_unit
        else
            write_openrc_script
        fi
        enable_service
    fi

    step "启动服务"
    start_service
    sleep 2

    if [ "$INIT" = "unknown" ]; then
        print_result "已安装（未配置自启）"
        note "请手动运行：${BIN_PATH}"
        return 0
    fi

    if service_running; then
        print_result "安装成功，服务运行中"
        return 0
    fi

    print_result "服务未能正常启动"
    note "查看日志排查："
    if [ "$INIT" = "systemd" ]; then
        note "  journalctl -u ${SERVICE_NAME} -n 50 --no-pager"
    else
        note "  cat ${LOG_FILE}"
    fi
    return 1
}

do_restart() {
    print_banner
    step "重启服务"
    require_root
    detect_init
    if ! service_installed; then
        fail "服务尚未安装，请先执行安装。"
        return 1
    fi
    stop_service
    sleep 1
    start_service
    sleep 2
    if service_running; then
        ok "服务已重启"
        _ip=$(get_ip)
        note "访问地址  http://${_ip}:${PORT}"
        return 0
    fi
    fail "重启失败，请查看日志。"
    if [ "$INIT" = "systemd" ]; then
        note "  journalctl -u ${SERVICE_NAME} -n 50 --no-pager"
    else
        note "  cat ${LOG_FILE}"
    fi
    return 1
}

do_status() {
    print_banner
    step "运行状态"
    detect_init

    if ! service_installed && [ ! -f "$BIN_PATH" ]; then
        warn "${APP_TITLE} 尚未安装。"
        return 1
    fi

    _ip=$(get_ip)
    printf '  %s初始化系统%s   %s\n'   "$DIM" "$RESET" "$INIT"
    printf '  %s程序路径%s     %s\n'   "$DIM" "$RESET" "$BIN_PATH"
    printf '  %s服务状态%s     %s\n'   "$DIM" "$RESET" "$(status_text)"
    printf '  %s开机自启%s     %s\n'   "$DIM" "$RESET" "$(autostart_text)"
    printf '  %s访问地址%s     %shttp://%s:%s%s\n' "$DIM" "$RESET" "$CYAN" "$_ip" "$PORT" "$RESET"
    line
    return 0
}

do_uninstall() {
    print_banner
    step "卸载 ${APP_TITLE}"
    require_root
    detect_init

    if ! service_installed && [ ! -f "$BIN_PATH" ]; then
        warn "未检测到已安装的 ${APP_TITLE}。"
        return 0
    fi

    if [ -t 0 ]; then
        printf '  %s确认卸载？此操作不可撤销 [y/N]: %s' "$YELLOW" "$RESET"
        _ans=''
        read -r _ans || true
        case "$_ans" in
            y|Y|yes|YES) ;;
            *) info "已取消。"; return 0 ;;
        esac
    fi

    case "$INIT" in
        systemd)
            systemctl stop "$SERVICE_NAME" >/dev/null 2>&1
            systemctl disable "$SERVICE_NAME" >/dev/null 2>&1
            rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
            systemctl daemon-reload >/dev/null 2>&1
            systemctl reset-failed "$SERVICE_NAME" >/dev/null 2>&1
            ok "服务已停止并移除 (systemd)"
            ;;
        openrc)
            rc-service "$SERVICE_NAME" stop >/dev/null 2>&1
            rc-update del "$SERVICE_NAME" default >/dev/null 2>&1
            rm -f "/etc/init.d/${SERVICE_NAME}"
            ok "服务已停止并移除 (OpenRC)"
            ;;
    esac

    if [ -f "$BIN_PATH" ]; then
        rm -f "$BIN_PATH"
        ok "程序文件已删除"
    fi

    printf '\n'
    line
    printf '  %s✔%s  %s已彻底卸载%s\n' "$GREEN" "$RESET" "$BOLD" "$RESET"
    line
    note "如需一并删除数据与日志，请执行："
    note "  rm -rf ${DATA_DIR} ${LOG_FILE}"
    printf '\n'
    return 0
}

do_purge() {
    do_uninstall || return 1
    rm -rf "$DATA_DIR" "$LOG_FILE" 2>/dev/null
    ok "数据目录与日志已清除"
    return 0
}

# ─────────────────────────────────────────────────────────────────── 入口 ──
show_menu() {
    require_root
    while :; do
        print_banner
        print_menu
        printf '\n  %s请输入选项%s %s[0-4]%s: ' "$BOLD" "$RESET" "$GRAY" "$RESET"
        read -r _choice || exit 0
        case "$_choice" in
            1) do_install; pause ;;
            2) do_restart; pause ;;
            3) do_status;  pause ;;
            4) do_uninstall; pause ;;
            0|q|Q) clear_screen; exit 0 ;;
            *) fail "无效输入，请重新选择"; sleep 1 ;;
        esac
    done
}

main() {
    setup_colors
    case "${1:-}" in
        install|update|i) require_root; do_install ;;
        restart|r)        require_root; do_restart ;;
        status|s)         do_status ;;
        uninstall|rm)     require_root; do_uninstall ;;
        purge)            require_root; do_purge ;;
        menu|"")          show_menu ;;
        -h|--help|help)   usage ;;
        *) fail "未知命令: $1"; usage; exit 1 ;;
    esac
}

main "$@"
