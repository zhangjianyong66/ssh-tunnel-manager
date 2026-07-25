#!/bin/sh

set -eu

usage() {
	printf '用法：%s <版本，例如 v1.0.0>\n' "$0" >&2
	exit 2
}

[ "$#" -eq 1 ] || usage
version=$1
if ! printf '%s\n' "$version" | grep -Eq '^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'; then
	printf '版本格式无效：%s\n' "$version" >&2
	exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
dist_dir="$repo_dir/dist"
release_version=${version#v}
amd64_package="ssh-tunnel-manager_${release_version}_linux_amd64"
arm64_package="ssh-tunnel-manager_${release_version}_linux_arm64"

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/ssh-tunnel-manager-release.XXXXXX")
checksums_tmp=''
cleanup() {
	rm -rf -- "$work_dir"
	[ -z "$checksums_tmp" ] || rm -f -- "$checksums_tmp"
}
trap cleanup EXIT HUP INT TERM

mkdir -p -- "$dist_dir"

for arch in amd64 arm64; do
	package="ssh-tunnel-manager_${release_version}_linux_${arch}"
	package_dir="$work_dir/$package"
	archive="$package.tar.gz"
	mkdir -p -- "$package_dir"

	(
		cd "$repo_dir"
		CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
			-trimpath \
			-ldflags "-s -w -X main.version=$version" \
			-o "$package_dir/ssh-tunnel-manager" \
			./cmd/ssh-tunnel-manager
	)
	install -m 0755 -- "$repo_dir/packaging/install.sh" "$package_dir/install.sh"
	install -m 0755 -- "$repo_dir/packaging/uninstall.sh" "$package_dir/uninstall.sh"
	install -m 0644 -- "$repo_dir/packaging/ssh-tunnel-manager.desktop" "$package_dir/ssh-tunnel-manager.desktop"
	install -m 0644 -- "$repo_dir/README.md" "$package_dir/README.md"
	install -m 0644 -- "$repo_dir/LICENSE" "$package_dir/LICENSE"

	tar \
		--sort=name \
		--mtime='UTC 1970-01-01' \
		--owner=0 \
		--group=0 \
		--numeric-owner \
		-C "$work_dir" \
		-czf "$work_dir/$archive" \
		"$package"
	mv -f -- "$work_dir/$archive" "$dist_dir/$archive"
done

checksums_tmp=$(mktemp "$dist_dir/.checksums.XXXXXX")
(
	cd "$dist_dir"
	sha256sum "$amd64_package.tar.gz" "$arm64_package.tar.gz"
) >"$checksums_tmp"
mv -f -- "$checksums_tmp" "$dist_dir/checksums.txt"

trap - EXIT HUP INT TERM
cleanup
printf '发布产物已写入：%s\n' "$dist_dir"
