#!/usr/bin/env bash
#
# 一键把本仓库发布到 GitHub。
#
# 用法：
#   ./scripts/publish.sh <GitHub用户名> [仓库名] [提交邮箱]
#
# 示例：
#   ./scripts/publish.sh zhangsan
#   ./scripts/publish.sh zhangsan inventory zhangsan@example.com
#
# 脚本会：
#   1. 把 README.md 里的 OWNER/REPO 占位符替换成真实地址
#   2. 配置本仓库的提交身份（不改动你的全局 git 配置）
#   3. 若检测到 gh CLI 且已登录，则直接创建远程仓库并推送
#      否则设置好 origin 远程地址并打印手动推送命令
#
set -euo pipefail

OWNER="${1:-}"
REPO="${2:-inventory}"
EMAIL="${3:-}"

if [ -z "$OWNER" ]; then
	echo "用法: $0 <GitHub用户名> [仓库名] [提交邮箱]" >&2
	echo "示例: $0 zhangsan inventory zhangsan@example.com" >&2
	exit 1
fi

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

echo "==> 仓库目录: $ROOT"

# ---------------------------------------------------------------- 1. 替换占位符
if grep -q "OWNER/REPO" README.md 2>/dev/null; then
	echo "==> 替换 README.md 中的 OWNER/REPO 占位符 -> $OWNER/$REPO"
	if sed --version >/dev/null 2>&1; then
		sed -i "s|OWNER/REPO|$OWNER/$REPO|g" README.md        # GNU sed
	else
		sed -i '' "s|OWNER/REPO|$OWNER/$REPO|g" README.md      # BSD/macOS sed
	fi
	grep -n "github.com/$OWNER/$REPO" README.md | head -5
else
	echo "==> README.md 中已无 OWNER/REPO 占位符，跳过"
fi

# ---------------------------------------------------------------- 2. 提交身份
if [ -n "$EMAIL" ]; then
	echo "==> 配置本仓库提交身份: $OWNER <$EMAIL>"
	git config user.name "$OWNER"
	git config user.email "$EMAIL"
else
	echo "==> 未提供邮箱，沿用当前提交身份: $(git config user.name) <$(git config user.email)>"
fi

# 若 README 有改动则追加提交
if ! git diff --quiet -- README.md; then
	echo "==> 提交 README 改动"
	git add README.md
	git commit -q -m "docs: 更新仓库地址占位符为 $OWNER/$REPO"
fi

# ---------------------------------------------------------------- 3. 推送
REMOTE_URL="https://github.com/$OWNER/$REPO.git"

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
	echo "==> 检测到已登录的 gh CLI，自动创建远程仓库并推送"
	if git remote get-url origin >/dev/null 2>&1; then
		git remote set-url origin "$REMOTE_URL"
	else
		git remote add origin "$REMOTE_URL"
	fi
	gh repo create "$REPO" --public --source=. --remote=origin --push
	echo ""
	echo "✅ 发布完成：https://github.com/$OWNER/$REPO"
	echo "   前往 Actions 页面查看构建结果："
	echo "   https://github.com/$OWNER/$REPO/actions"
else
	echo "==> 未检测到可用的 gh CLI，已为你准备好远程地址"
	if git remote get-url origin >/dev/null 2>&1; then
		git remote set-url origin "$REMOTE_URL"
	else
		git remote add origin "$REMOTE_URL"
	fi
	echo ""
	echo "接下来请在 GitHub 上手动完成最后两步："
	echo ""
	echo "  1) 打开 https://github.com/new 新建仓库"
	echo "     - Repository name: $REPO"
	echo "     - 可见性按需选择（Public / Private）"
	echo "     - ⚠️ 不要勾选 Add a README / .gitignore / license（本地已有）"
	echo ""
	echo "  2) 回到本目录执行："
	echo "     git push -u origin main"
	echo ""
	echo "  推送后前往 https://github.com/$OWNER/$REPO/actions 查看 CI 结果。"
	echo "  首次推送会触发 CI；之后打标签即可触发多平台发布："
	echo "     git tag v1.0.0 && git push origin v1.0.0"
fi
