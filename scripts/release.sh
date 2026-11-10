#!/usr/bin/env bash
# 打 tag 并推送，触发 .github/workflows/release.yml 发布。
#
#   scripts/release.sh            只做检查并打印将要执行的动作（dry-run）
#   scripts/release.sh --push     真正打 tag 并 push
#
# 版本号来源是 mygo.json 的 version，tag 由它生成（v1.0.0），两者必须一致。
set -euo pipefail

cd "$(dirname "$0")/.."

PUSH=0
for arg in "$@"; do
	case "$arg" in
	--push) PUSH=1 ;;
	-h | --help)
		sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "未知参数：$arg" >&2
		exit 2
		;;
	esac
done

fail() {
	echo "✗ $*" >&2
	exit 1
}

version="$(jq -r .version mygo.json)"
[ -n "$version" ] && [ "$version" != "null" ] || fail "mygo.json 里没有 version"
tag="v$version"

echo "版本   $version"
echo "tag    $tag"

# 1. 工作区必须干净，否则打出来的 tag 和代码对不上
[ -z "$(git status --porcelain)" ] || fail "工作区有未提交改动，先提交再发版"

# 2. tag 不能已经存在
if git rev-parse -q --verify "refs/tags/$tag" > /dev/null; then
	fail "本地已存在 tag $tag"
fi
# 远端检查走 push 用的地址：这个网络下 fetch 的 HTTPS 会 SSL 握手失败，SSH 正常。
remote_url="$(git remote get-url --push origin)"
if [ -n "$(git ls-remote --tags "$remote_url" "refs/tags/$tag" 2>/dev/null)" ]; then
	fail "远端已存在 tag $tag"
fi

# 3. 分支要对，避免从临时分支发版
branch="$(git rev-parse --abbrev-ref HEAD)"
echo "分支   $branch"

# 4. 本地能编译能过测试
echo "→ go vet / go test"
go vet ./... > /dev/null
go test ./... > /dev/null

# 5. 更新签名私钥（CI 用 secrets.MYGO_UPDATER_PRIVATE_KEY，走同一条流水线）
key="${HOME}/Library/Application Support/mygo/update-keys/mygo-update.key"
if [ -f "$key" ]; then
	echo "密钥   $key"
else
	echo "提示   本地没有 $key；CI 发布需要仓库 secret MYGO_UPDATER_PRIVATE_KEY"
fi

if [ "$PUSH" -eq 0 ]; then
	echo
	echo "检查通过。真正发版请执行：scripts/release.sh --push"
	exit 0
fi

# 6. 打 tag 并推送，之后由 GitHub Actions 建草稿 release → 构建上传 → 发布
git tag -a "$tag" -m "$tag"
git push origin "$tag"

echo
echo "已推送 ${tag}。到 GitHub Actions 看进度，完成后 App 内的「检查更新」即可发现新版本。"
