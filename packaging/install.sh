#!/bin/sh

set -eu

fail() {
	printf '安装失败：%s\n' "$1" >&2
	exit 1
}

normalize_user_path() {
	resolved=$(realpath -m -- "$2") || fail "无法解析 $1：$2"
	case "$resolved" in
		"$home_dir"/*) printf '%s\n' "$resolved" ;;
		*) fail "$1 必须位于当前用户 HOME 下：$2" ;;
	esac
}

escape_desktop_exec() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/`/\\`/g; s/\$/\\$/g'
}

[ -n "${HOME:-}" ] || fail 'HOME 未设置'
home_dir=$(realpath -m -- "$HOME") || fail "无法解析 HOME：$HOME"
[ "$home_dir" != / ] || fail 'HOME 不能是根目录'

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source_binary="$script_dir/ssh-tunnel-manager"
source_uninstaller="$script_dir/uninstall.sh"
source_desktop="$script_dir/ssh-tunnel-manager.desktop"

[ -f "$source_binary" ] || fail "发布包中缺少 ssh-tunnel-manager：$script_dir"
[ -f "$source_uninstaller" ] || fail "发布包中缺少 uninstall.sh：$script_dir"
[ -f "$source_desktop" ] || fail "发布包中缺少桌面入口模板：$script_dir"

bin_dir=$(normalize_user_path XDG_BIN_HOME "${XDG_BIN_HOME:-"$HOME/.local/bin"}")
data_home=$(normalize_user_path XDG_DATA_HOME "${XDG_DATA_HOME:-"$HOME/.local/share"}")
applications_dir="$data_home/applications"
binary_path="$bin_dir/ssh-tunnel-manager"
uninstaller_path="$bin_dir/ssh-tunnel-manager-uninstall"
desktop_path="$applications_dir/ssh-tunnel-manager.desktop"

mkdir -p -- "$bin_dir" "$applications_dir"

binary_tmp=''
uninstaller_tmp=''
desktop_tmp=''
cleanup() {
	[ -z "$binary_tmp" ] || rm -f -- "$binary_tmp"
	[ -z "$uninstaller_tmp" ] || rm -f -- "$uninstaller_tmp"
	[ -z "$desktop_tmp" ] || rm -f -- "$desktop_tmp"
}
trap cleanup EXIT HUP INT TERM
binary_tmp=$(mktemp "$bin_dir/.ssh-tunnel-manager.XXXXXX")
uninstaller_tmp=$(mktemp "$bin_dir/.ssh-tunnel-manager-uninstall.XXXXXX")
desktop_tmp=$(mktemp "$applications_dir/.ssh-tunnel-manager.desktop.XXXXXX")

install -m 0755 -- "$source_binary" "$binary_tmp"
install -m 0755 -- "$source_uninstaller" "$uninstaller_tmp"

escaped_binary_path=$(escape_desktop_exec "$binary_path")
found_exec=false
while IFS= read -r line || [ -n "$line" ]; do
	if [ "$line" = 'Exec=@EXEC@ --open-browser' ]; then
		printf 'Exec="%s" --open-browser\n' "$escaped_binary_path" >>"$desktop_tmp"
		found_exec=true
	else
		printf '%s\n' "$line" >>"$desktop_tmp"
	fi
done <"$source_desktop"
[ "$found_exec" = true ] || fail '桌面入口模板缺少 Exec 占位符'
chmod 0644 "$desktop_tmp"

mv -f -- "$binary_tmp" "$binary_path"
mv -f -- "$uninstaller_tmp" "$uninstaller_path"
mv -f -- "$desktop_tmp" "$desktop_path"
trap - EXIT HUP INT TERM

if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database "$applications_dir" >/dev/null 2>&1 || :
fi

printf '安装完成。\n命令：%s\n桌面入口：%s\n卸载命令：%s\n' "$binary_path" "$desktop_path" "$uninstaller_path"
