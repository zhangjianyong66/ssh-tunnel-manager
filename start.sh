#!/bin/sh

set -eu

fail() {
	printf '启动失败：%s\n' "$1" >&2
	exit 1
}

command -v go >/dev/null 2>&1 || fail '未找到 Go，请先安装 Go 1.22 或更高版本'
command -v ssh >/dev/null 2>&1 || fail '未找到 OpenSSH 客户端，请先安装 ssh 命令'

go_version=$(go env GOVERSION 2>/dev/null) || fail '无法读取 Go 版本'
go_major=${go_version#go}
go_major=${go_major%%.*}
go_minor=${go_version#go*.}
go_minor=${go_minor%%[!0-9]*}

case "$go_major:$go_minor" in
	*[!0-9:]* | :* | *:) fail "无法识别 Go 版本：$go_version" ;;
esac
if [ "$go_major" -lt 1 ] || { [ "$go_major" -eq 1 ] && [ "$go_minor" -lt 22 ]; }; then
	fail "Go 版本过低：$go_version，请安装 Go 1.22 或更高版本"
fi

if ! command -v xdg-open >/dev/null 2>&1; then
	printf '提示：未找到 xdg-open，浏览器不会自动打开，请使用程序输出的控制台网址。\n' >&2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || fail '无法定位项目目录'
cd "$script_dir" || fail "无法进入项目目录：$script_dir"

exec go run ./cmd/ssh-tunnel-manager --open-browser "$@"
