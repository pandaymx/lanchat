# LAN Chat —— Makefile
# Go 侧任务入口。Node 侧见 package.json。

SHELL := /bin/bash

# 版本号来自 git，不写死在源码里（见 AGENTS.md §6）
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

BIN_DIR := bin

.PHONY: all build test lint fmt fmt-check tidy templ clean run-hub run-tui hooks

all: lint test build

## build: 构建全部 cmd 到 bin/
## 依赖 templ：*_templ.go 被 .gitignore 排除，不先生成就会编译失败
build: templ
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ ./cmd/...

## test: 跑全量测试（带竞态检测）
## 同样依赖 templ，理由同 build
test: templ
	go test -race ./...

## lint: 静态检查
lint:
	golangci-lint run

## fmt: 格式化全部（Go + templ）
fmt:
	gofumpt -l -w .
	gci write --no-lex-order -s standard -s default -s "prefix(github.com/pandaymx/lanchat)" .
	@templ fmt . 2>/dev/null || true

## tidy: 整理依赖
tidy:
	go mod tidy

## templ: 编译 .templ 为 _templ.go（改了模板必须跑）
templ:
	templ generate

## run-hub: 运行服务端
run-hub:
	go run -ldflags "$(LDFLAGS)" ./cmd/hub

## run-tui: 运行终端客户端
run-tui:
	go run -ldflags "$(LDFLAGS)" ./cmd/tui

## run-web: 运行浏览器端（默认 :9001；需先起 hub）
## 用法：make run-web HUB=ws://127.0.0.1:9000/ws USER=alice
HUB ?= ws://127.0.0.1:9000/ws
USER ?= alice
run-web: templ
	go run -ldflags "$(LDFLAGS)" ./cmd/web -hub-url "$(HUB)" -user "$(USER)"

## hooks: 安装 git 钩子（clone 后必做）
hooks:
	lefthook install

## clean: 清理构建产物
clean:
	rm -rf $(BIN_DIR)
