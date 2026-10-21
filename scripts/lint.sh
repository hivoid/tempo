#!/usr/bin/env bash
# lint.sh — 按平台分别运行静态检查。
#
# 带 //go:build windows 的文件（internal/platform/service_windows.go、
# internal/transport/pipe_windows.go、internal/notify/notify_windows.go、
# internal/daemon/prompt_windows.go）只能在其依赖包可编译的 GOOS 下检查：
# 单独 lint 一个异平台文件会因构建约束排除全部依赖文件而报
# "could not import ... build constraints exclude all Go files"，属假阳性。
# 因此这里固定跑两轮：宿主平台与 GOOS=windows。

set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> go vet (host: $(go env GOOS))"
go vet ./...

echo "==> go test (host: $(go env GOOS))"
go test ./...

echo "==> golangci-lint (host: $(go env GOOS))"
golangci-lint run ./...

echo "==> go build (GOOS=windows)"
GOOS=windows go build ./...

echo "==> golangci-lint (GOOS=windows)"
GOOS=windows golangci-lint run ./...
