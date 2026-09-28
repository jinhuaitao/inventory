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
	internal/database/schema.sql
	internal/handlers/routes.go
	internal/services/security.go
	internal/updater/updater.go
	internal/updater/apply.go
	internal/updater/semver.go
	internal/updater/asset_test.go
	web/templates/security.html
	web/templates/update/index.html
)

echo "==> 1/4 必需文件"
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

echo "==> 2/4 已废弃源码文件（残留会导致编译失败）"
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
echo

# ---------------------------------------------------------------- 3. Git 规则
echo "==> 3/4 忽略规则与构建产物"
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
echo "==> 4/4 编译与静态检查"
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

if [ "$fail" -eq 0 ]; then
	echo "✅ 体检通过：代码树完整且可编译。"
else
	echo "❌ 体检发现问题，请按上面的提示修复。"
fi
exit "$fail"
