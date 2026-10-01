#!/usr/bin/env bash
#
# 代码树体检。
#
# 为什么需要它：
# 用 GitHub 网页的「上传文件」更新代码时，有两个绕不开的限制 ——
#   1. 网页上传只能新增/覆盖文件，无法删除文件；
#   2. 以 . 开头的隐藏文件与目录（.gitignore、.github/）在拖拽时常常被系统跳过。
# 于是目录里会同时出现「旧版本残留」与「新文件缺失」两类问题：
# 前者导致 go build 报一堆 undefined，后者导致 CI 根本不触发。
#
# 本脚本把这些问题一次性检查出来，最后再跑一遍 gofmt / go vet / go build。
#
# 用法：
#   ./scripts/doctor.sh
#
# 退出码：0 表示全部通过，1 表示存在问题。

set -uo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

fail=0

echo "==> 检查目录: $ROOT"
echo

# ---------------------------------------------------------------- 1. 必需文件
# 这些文件缺失会导致编译失败、CI 不触发或部署不可用。
REQUIRED=(
	cmd/server/main.go
	go.mod
	go.sum
	Makefile
	README.md
	LICENSE
	.gitignore
	.github/workflows/ci.yml
	internal/config/config.go
	internal/database/database.go
	internal/database/schema.sql
	internal/database/database_test.go
	internal/handlers/routes.go
	internal/services/security.go
	internal/updater/updater.go
	internal/updater/apply.go
	internal/updater/semver.go
	internal/updater/fsutil.go
	internal/updater/replace_unix.go
	internal/updater/replace_windows.go
	internal/updater/asset_test.go
	internal/updater/restart_test.go
	web/templates/security.html
	web/templates/update/index.html
	deploy/install.sh
	deploy/README.md
	deploy/inventory.env.example
	deploy/systemd/inventory.service
	deploy/systemd/inventory-backup.service
	deploy/systemd/inventory-backup.timer
	deploy/openrc/inventory
	deploy/openrc/inventory.confd
	deploy/openrc/periodic-daily-backup
)

echo "==> 1/5 必需文件"
missing=0
for f in "${REQUIRED[@]}"; do
	if [ -f "$f" ]; then
		printf '  ✓ %s\n' "$f"
	else
		printf '  ✗ 缺失: %s\n' "$f"
		missing=$((missing + 1))
	fi
done
if [ "$missing" -gt 0 ]; then
	fail=1
	echo
	echo "  提示：.gitignore 与 .github/ 是隐藏文件，网页拖拽上传时最容易被漏掉。"
fi
echo

# ---------------------------------------------------------------- 2. 废弃源码
# 这些文件在新版本中已删除。残留会导致编译失败 —— 因为新版的 config
# 已经移除了 SMTP 相关字段，而旧版 mailer.go 仍在引用它们。
OBSOLETE=(
	internal/auth/mailer.go
	internal/auth/templates.go
	web/templates/auth/reset.html
)

echo "==> 2/5 已废弃源码文件（残留会导致编译失败）"
stale=0
for f in "${OBSOLETE[@]}"; do
	if [ -e "$f" ]; then
		printf '  ✗ 应删除: %s\n' "$f"
		stale=$((stale + 1))
	fi
done
if [ "$stale" -eq 0 ]; then
	echo "  ✓ 无残留"
else
	fail=1
	echo
	echo "  一键修复："
	echo "    rm -f ${OBSOLETE[*]}"
fi

# 编辑器原子保存的残留文件：形如 database_test.gorlzy4f（文件名 .go 后拖着随机后缀）。
# 它们不以 .go 结尾，go 工具链会直接无视，却能一路混进发布目录。
# *.go?* 会命中这类残留；已知例外 .golangci.yml 单独放行。
artifacts="$(find cmd internal web scripts deploy -type f \
	\( -name '*.go?*' -o -name '*~' -o -name '*.bak' -o -name '*.tmp' \) \
	! -name '.golangci.yml' 2>/dev/null || true)"
if [ -n "$artifacts" ]; then
	fail=1
	echo "  ✗ 发现编辑器/临时残留文件，应当删除："
	echo "$artifacts" | sed 's/^/      /'
	echo "    修复方式：逐个确认后 rm -f 删除，并把 *.gorlzy* / *~ / *.bak / *.tmp 加进 .gitignore。"
else
	echo "  ✓ 无编辑器残留文件"
fi
echo

# ---------------------------------------------------------------- 3. Git 规则
echo "==> 3/5 忽略规则与构建产物"
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	# 关键守卫：.gitignore 里若出现不带 / 的裸模式（如 server），
	# 会匹配任意层级同名目录，从而把 cmd/server/ 这类源码目录一起忽略掉。
	if git check-ignore -q cmd/server/main.go 2>/dev/null; then
		fail=1
		echo "  ✗ .gitignore 把入口文件 cmd/server/main.go 忽略了！"
		echo "    原因通常是规则里出现了不带 / 的裸模式。用下面的命令定位："
		echo "      git check-ignore -v cmd/server/main.go"
		echo "    修复方式：把该规则改成以 / 锚定仓库根目录的形式。"
	else
		echo "  ✓ 源码未被 .gitignore 误伤"
	fi

	# 构建产物与运行时数据不应进入版本控制。
	tracked_artifacts="$(git ls-files 2>/dev/null | grep -E '^(bin|dist)/|\.(db|db-wal|db-shm|sqlite3?)$' || true)"
	if [ -n "$tracked_artifacts" ]; then
		fail=1
		echo "  ✗ 以下构建产物/数据文件已被纳入版本控制，应当移除："
		echo "$tracked_artifacts" | sed 's/^/      /'
		echo "    修复方式："
		echo "      git rm --cached <文件>   # 从版本控制移除，保留本地文件"
	else
		echo "  ✓ 构建产物未进入版本控制"
	fi
else
	echo "  ⚠ 当前目录不是 Git 仓库，跳过本节"
fi
echo

# ---------------------------------------------------------------- 4. 编译检查
echo "==> 4/5 编译与静态检查"
if ! command -v go >/dev/null 2>&1; then
	echo "  ⚠ 未找到 go，跳过本节"
else
	unformatted="$(gofmt -l ./cmd ./internal ./web 2>/dev/null)"
	if [ -n "$unformatted" ]; then
		echo "  ✗ 以下文件未格式化（运行 gofmt -w ./cmd ./internal ./web 修复）："
		echo "$unformatted" | sed 's/^/      /'
		fail=1
	else
		echo "  ✓ gofmt"
	fi

	if go vet ./... >/dev/null 2>&1; then
		echo "  ✓ go vet"
	else
		echo "  ✗ go vet 未通过，详情请运行：go vet ./..."
		fail=1
	fi

	tmpbin="$(mktemp -t inventory-doctor.XXXXXX)"
	if go build -o "$tmpbin" ./cmd/server 2>/dev/null; then
		echo "  ✓ go build"
	else
		echo "  ✗ go build 失败，详情请运行：go build ./cmd/server"
		fail=1
	fi
	rm -f "$tmpbin"
fi
echo

# ---------------------------------------------------------------- 5. 部署资产
# 这些检查对应最容易出错、且出错了很难在生产现场定位的几点：
# 脚本语法、可执行位、以及「自更新要求安装目录可写」这条硬前提。
echo "==> 5/5 部署资产（Debian / Alpine）"
deploy_issues=0

for s in deploy/install.sh deploy/openrc/inventory deploy/openrc/periodic-daily-backup; do
	[ -f "$s" ] || continue
	if sh -n "$s" 2>/dev/null; then
		echo "  ✓ 语法 $s"
	else
		echo "  ✗ 语法错误: $s（运行 sh -n $s 查看详情）"
		deploy_issues=$((deploy_issues + 1))
	fi
done

for s in deploy/install.sh deploy/openrc/inventory deploy/openrc/periodic-daily-backup; do
	if [ -f "$s" ] && [ ! -x "$s" ]; then
		echo "  ✗ $s 缺少可执行权限（chmod +x $s）"
		deploy_issues=$((deploy_issues + 1))
	fi
done

if [ -f deploy/systemd/inventory.service ]; then
	if grep -q '^ReadWritePaths=.*/opt/inventory' deploy/systemd/inventory.service; then
		echo "  ✓ systemd 单元已允许写入安装目录（自更新前提）"
	else
		echo "  ✗ systemd 单元缺少 ReadWritePaths=/opt/inventory，在线更新会因权限失败"
		deploy_issues=$((deploy_issues + 1))
	fi
fi

# 备份单元同样依赖 ReadWritePaths：ProtectSystem=strict 下不显式开洞就写不出备份
if [ -f deploy/systemd/inventory-backup.service ]; then
	if grep -q '^ReadWritePaths=' deploy/systemd/inventory-backup.service; then
		echo "  ✓ 备份单元已放行备份目录"
	else
		echo "  ✗ 备份单元缺少 ReadWritePaths，ProtectSystem=strict 下备份会失败"
		deploy_issues=$((deploy_issues + 1))
	fi
fi

for f in deploy/inventory.env.example deploy/openrc/inventory.confd; do
	if [ -f "$f" ] && ! grep -q '^INVENTORY_SESSION_SECRET=' "$f"; then
		echo "  ✗ $f 缺少 INVENTORY_SESSION_SECRET 条目"
		deploy_issues=$((deploy_issues + 1))
	fi
done

if [ "$deploy_issues" -eq 0 ]; then
	echo "  ✓ 部署资产检查通过"
else
	fail=1
fi
echo

if [ "$fail" -eq 0 ]; then
	echo "✅ 体检通过：代码树完整且可编译。"
else
	echo "❌ 体检发现问题，请按上面的提示修复。"
fi
exit "$fail"
