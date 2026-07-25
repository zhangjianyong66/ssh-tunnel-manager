#!/bin/sh

set -eu

fail() {
	printf '卸载失败：%s\n' "$1" >&2
	exit 1
}

normalize_user_path() {
	resolved=$(realpath -m -- "$2") || fail "无法解析 $1：$2"
	case "$resolved" in
		"$home_dir"/*) printf '%s\n' "$resolved" ;;
		*) fail "$1 必须位于当前用户 HOME 下：$2" ;;
	esac
}

[ -n "${HOME:-}" ] || fail 'HOME 未设置'
home_dir=$(realpath -m -- "$HOME") || fail "无法解析 HOME：$HOME"
[ "$home_dir" != / ] || fail 'HOME 不能是根目录'

bin_dir=$(normalize_user_path XDG_BIN_HOME "${XDG_BIN_HOME:-"$HOME/.local/bin"}")
data_home=$(normalize_user_path XDG_DATA_HOME "${XDG_DATA_HOME:-"$HOME/.local/share"}")
applications_dir="$data_home/applications"
binary_path="$bin_dir/ssh-tunnel-manager"
uninstaller_path="$bin_dir/ssh-tunnel-manager-uninstall"
desktop_path="$applications_dir/ssh-tunnel-manager.desktop"

rm -f -- "$binary_path" "$desktop_path" "$uninstaller_path"

if command -v update-desktop-database >/dev/null 2>&1 && [ -d "$applications_dir" ]; then
	update-desktop-database "$applications_dir" >/dev/null 2>&1 || :
fi

printf '卸载完成。用户配置、SSH 配置和系统密钥环凭据均已保留。\n'
