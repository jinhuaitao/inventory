# 库存管理系统 —— 开发与构建辅助
#
# 常用命令：
#   make help        查看全部命令
#   make run         本地运行
#   make test        运行单元测试
#   make check       提交前的完整检查（格式 + 静态分析 + 测试）
#   make release-artifacts  生成与 CI 一致的发布产物（Linux amd64/arm64 + checksums.txt）

APP_NAME    := inventory-server
CMD_PKG     := ./cmd/server
BIN_DIR     := bin
DIST_DIR    := dist

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# 更新仓库（owner/repo）。编译进二进制后，程序无需环境变量即可自助检查更新。
UPDATE_REPO ?= jinhuaitao/inventory

LDFLAGS     := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(BUILD_DATE) \
	-X main.updateRepo=$(UPDATE_REPO)

GO          ?= go
GOFLAGS     := -trimpath

.DEFAULT_GOAL := help
.PHONY: help run build release-artifacts update-check test race cover vet fmt fmt-check tidy check doctor clean release-snapshot

## help: 显示所有可用命令
help:
	@echo "库存管理系统 —— 可用命令："
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | awk -F': ' '{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
	@echo ""

## run: 本地启动开发服务器
run:
	$(GO) run $(CMD_PKG)

## build: 编译当前平台可执行文件到 bin/
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP_NAME) $(CMD_PKG)
	@echo "已生成 $(BIN_DIR)/$(APP_NAME)（版本 $(VERSION)）"

## update-check: 检查是否有新版本（需要配置 INVENTORY_UPDATE_REPO）
update-check:
	@$(GO) run -ldflags "-X main.version=$(VERSION)" $(CMD_PKG) --check-update

## release-artifacts: 生成与 CI 一致的发布产物到 dist/（Linux amd64/arm64 + checksums.txt）
release-artifacts: clean-dist
	@mkdir -p $(DIST_DIR)
	@set -e; \
	for arch in amd64 arm64; do \
		echo "编译 linux/$$arch ..."; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch \
			$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
			-o "$(DIST_DIR)/$(APP_NAME)-$$arch" $(CMD_PKG); \
	done
	@cd $(DIST_DIR) && shasum -a 256 $(APP_NAME)-amd64 $(APP_NAME)-arm64 > checksums.txt
	@echo "发布产物已生成于 $(DIST_DIR)/："
	@ls -1 $(DIST_DIR) | sed 's/^/  /'
	@echo "（只发布 Linux 两个架构，与 CI 的 release 作业保持一致）"

## test: 运行单元测试
test:
	$(GO) test -count=1 ./...

## race: 带竞态检测运行测试
race:
	$(GO) test -race -count=1 ./...

## cover: 生成测试覆盖率报告
cover:
	$(GO) test -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -n 20
	@echo "详细报告：$(GO) tool cover -html=coverage.out"

## vet: 运行静态分析
vet:
	$(GO) vet ./...

## fmt: 格式化全部 Go 代码
fmt:
	$(GO) fmt ./cmd/... ./internal/... ./web/...

## fmt-check: 检查代码格式（CI 使用）
fmt-check:
	@unformatted="$$(gofmt -l ./cmd ./internal ./web)"; \
	if [ -n "$$unformatted" ]; then \
		echo "以下文件未通过 gofmt 格式化："; echo "$$unformatted"; exit 1; \
	fi; \
	echo "所有 Go 文件格式正确"

## tidy: 整理依赖
tidy:
	$(GO) mod tidy

## check: 提交前的完整检查（格式 + 静态分析 + 测试）
check: fmt-check vet test
	@echo "检查全部通过 ✓"

## doctor: 代码树体检（缺失/残留文件、忽略规则、编译）
doctor:
	@./scripts/doctor.sh

## clean-dist: 清理 dist/ 目录
clean-dist:
	@rm -rf $(DIST_DIR)

## clean: 清理构建产物
clean: clean-dist
	@rm -rf $(BIN_DIR) coverage.out coverage.html
	@echo "已清理构建产物"
