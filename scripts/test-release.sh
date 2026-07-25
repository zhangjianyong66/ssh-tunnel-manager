#!/bin/sh

set -eu

fail() {
	printf '发布回归失败：%s\n' "$1" >&2
	exit 1
}

[ "$#" -le 1 ] || fail '最多只能指定一个版本参数'
version=${1:-v0.0.0-test}
release_version=${version#v}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
dist_dir="$repo_dir/dist"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/ssh-tunnel-manager-release-test.XXXXXX")
cleanup() {
	rm -rf -- "$test_dir"
}
trap cleanup EXIT HUP INT TERM

"$script_dir/build-release.sh" "$version" >/dev/null

amd64_package="ssh-tunnel-manager_${release_version}_linux_amd64"
arm64_package="ssh-tunnel-manager_${release_version}_linux_arm64"
amd64_archive="$dist_dir/$amd64_package.tar.gz"
arm64_archive="$dist_dir/$arm64_package.tar.gz"
[ -f "$amd64_archive" ] || fail '缺少 amd64 压缩包'
[ -f "$arm64_archive" ] || fail '缺少 arm64 压缩包'
[ "$(wc -l <"$dist_dir/checksums.txt" | tr -d ' ')" = 2 ] || fail 'checksums.txt 不是两个条目'
(
	cd "$dist_dir"
	sha256sum -c checksums.txt
) >/dev/null || fail 'SHA-256 校验失败'

for package in "$amd64_package" "$arm64_package"; do
	archive="$dist_dir/$package.tar.gz"
	expected=$(printf '%s\n' \
		"$package/" \
		"$package/LICENSE" \
		"$package/README.md" \
		"$package/install.sh" \
		"$package/ssh-tunnel-manager" \
		"$package/ssh-tunnel-manager.desktop" \
		"$package/uninstall.sh")
	actual=$(tar -tzf "$archive" | sort)
	[ "$actual" = "$expected" ] || fail "$package 的文件清单不正确"
	tar -xzf "$archive" -C "$test_dir"
done

amd64_binary="$test_dir/$amd64_package/ssh-tunnel-manager"
arm64_binary="$test_dir/$arm64_package/ssh-tunnel-manager"
[ "$("$amd64_binary" --version)" = "$version" ] || fail 'amd64 程序版本与标签不一致'
file "$amd64_binary" | grep -Eq 'ELF 64-bit.*x86-64' || fail 'amd64 程序架构不正确'
file "$arm64_binary" | grep -Eq 'ELF 64-bit.*(ARM aarch64|ARM64)' || fail 'arm64 程序架构不正确'

printf '双架构发布回归通过。\n'
