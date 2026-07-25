#!/bin/sh

set -eu

fail() {
	printf '打包回归失败：%s\n' "$1" >&2
	exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/ssh-tunnel-manager-packaging-test.XXXXXX")
cleanup() {
	rm -rf -- "$test_dir"
}
trap cleanup EXIT HUP INT TERM

package_dir="$test_dir/release package"
test_home="$test_dir/home"
bin_dir="$test_home/bin with space"
data_home="$test_home/data with space"
config_home="$test_home/config"
mkdir -p -- "$package_dir" "$config_home/ssh-tunnel-manager"
install -m 0755 -- "$repo_dir/packaging/install.sh" "$package_dir/install.sh"
install -m 0755 -- "$repo_dir/packaging/uninstall.sh" "$package_dir/uninstall.sh"
install -m 0644 -- "$repo_dir/packaging/ssh-tunnel-manager.desktop" "$package_dir/ssh-tunnel-manager.desktop"
printf '#!/bin/sh\nprintf "first-version\\n"\n' >"$package_dir/ssh-tunnel-manager"
chmod 0755 "$package_dir/ssh-tunnel-manager"
printf 'keep\n' >"$config_home/ssh-tunnel-manager/config.json"

run_with_test_paths() {
	env \
		HOME="$test_home" \
		XDG_BIN_HOME="$bin_dir" \
		XDG_DATA_HOME="$data_home" \
		XDG_CONFIG_HOME="$config_home" \
		"$@"
}

run_with_test_paths "$package_dir/install.sh" >/dev/null
binary_path="$bin_dir/ssh-tunnel-manager"
uninstaller_path="$bin_dir/ssh-tunnel-manager-uninstall"
desktop_path="$data_home/applications/ssh-tunnel-manager.desktop"

[ -x "$binary_path" ] || fail '程序未安装为可执行文件'
[ -x "$uninstaller_path" ] || fail '卸载命令未安装为可执行文件'
[ -f "$desktop_path" ] || fail '桌面入口未安装'
[ "$(stat -c '%a' "$binary_path")" = 755 ] || fail '程序权限不是 0755'
[ "$(stat -c '%a' "$uninstaller_path")" = 755 ] || fail '卸载命令权限不是 0755'
[ "$(stat -c '%a' "$desktop_path")" = 644 ] || fail '桌面入口权限不是 0644'
grep -F "Exec=\"$binary_path\" --open-browser" "$desktop_path" >/dev/null || fail '桌面入口未使用安装后的绝对路径'
[ "$("$binary_path")" = first-version ] || fail '安装后的程序内容不正确'

printf '#!/bin/sh\nprintf "second-version\\n"\n' >"$package_dir/ssh-tunnel-manager"
chmod 0755 "$package_dir/ssh-tunnel-manager"
run_with_test_paths "$package_dir/install.sh" >/dev/null
[ "$("$binary_path")" = second-version ] || fail '重复安装未覆盖旧版本'

run_with_test_paths "$uninstaller_path" >/dev/null
[ ! -e "$binary_path" ] || fail '卸载后程序仍存在'
[ ! -e "$uninstaller_path" ] || fail '卸载后卸载命令仍存在'
[ ! -e "$desktop_path" ] || fail '卸载后桌面入口仍存在'
[ -f "$config_home/ssh-tunnel-manager/config.json" ] || fail '卸载误删了用户配置'
run_with_test_paths "$package_dir/uninstall.sh" >/dev/null

if run_with_test_paths env XDG_BIN_HOME=relative "$package_dir/install.sh" >/dev/null 2>&1; then
	fail '安装器接受了相对 XDG_BIN_HOME'
fi
if run_with_test_paths env XDG_DATA_HOME="$test_dir/outside-home" "$package_dir/install.sh" >/dev/null 2>&1; then
	fail '安装器接受了 HOME 外的 XDG_DATA_HOME'
fi

printf '安装与卸载回归通过。\n'
