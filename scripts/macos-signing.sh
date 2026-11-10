#!/usr/bin/env bash
# 管理本机的固定代码签名身份“Toolbox Local Dev”。
#
# 背景：macOS 的屏幕录制/辅助功能授权（TCC）按应用的 designated requirement
# 匹配。ad-hoc 签名（codesign -s -）的要求是 cdhash，每次构建都会变，所以
# 每次更新都要重新授权；固定的自签名证书则是 “bundle id + 证书指纹”，
# 只要证书不换，授权就能跨版本保留。
#
# 证书材料随仓库放在 signing/：
#   signing/toolbox-signing.p12  证书 + 私钥（仓库公开，仅用于个人项目）
#   signing/toolbox-signing.cer  公开证书
#
#   bash scripts/macos-signing.sh            导入仓库证书并信任
#   bash scripts/macos-signing.sh --import   同上
#   bash scripts/macos-signing.sh --export   从本机钥匙串导出到 signing/（--force 覆盖）
#   bash scripts/macos-signing.sh --verify   校验身份、指纹、有效期与已构建 app 的 DR
#   bash scripts/macos-signing.sh --remove   删除本机身份和信任，不动仓库文件
#
# 环境变量：
#   TOOLBOX_SIGNING_IDENTITY    默认 "Toolbox Local Dev"
#   TOOLBOX_SIGNING_KEYCHAIN    默认 $HOME/Library/Keychains/login.keychain-db
#   TOOLBOX_SIGNING_P12_PASSWORD 默认 toolbox-local-dev（见 signing/README.md）
#   TOOLBOX_SIGNING_MATERIAL_DIR 默认 <repo>/signing

set -euo pipefail

cd "$(dirname "$0")/.."

identity="${TOOLBOX_SIGNING_IDENTITY:-Toolbox Local Dev}"
keychain="${TOOLBOX_SIGNING_KEYCHAIN:-$HOME/Library/Keychains/login.keychain-db}"
material="${TOOLBOX_SIGNING_MATERIAL_DIR:-$PWD/signing}"
p12_password="${TOOLBOX_SIGNING_P12_PASSWORD:-toolbox-local-dev}"
p12="$material/toolbox-signing.p12"
cer="$material/toolbox-signing.cer"

# 仓库里这张证书的指纹。换证书时必须同步改这里，否则所有用户授权都会失效。
expected_sha1="1bda019447623096dbaf37354a7741f69151d8cb"

die() {
	echo "✗ $*" >&2
	exit 1
}

note() {
	echo "→ $*"
}

normalize_sha1() {
	tr -d ':' | tr 'A-F' 'a-f'
}

cert_sha1() {
	/usr/bin/openssl x509 -in "$1" -noout -fingerprint -sha1 2>/dev/null | sed 's/.*=//' | normalize_sha1
}

cert_expiry() {
	/usr/bin/openssl x509 -in "$1" -noout -enddate 2>/dev/null | sed 's/^notAfter=//'
}

# keychain_cert_sha1s 打印钥匙串中所有同名证书的 SHA-1，每行一个。
keychain_cert_sha1s() {
	local pem_block pem_file sha1
	pem_block="$(mktemp -d "${TMPDIR:-/tmp}/toolbox-cert.XXXXXX")"
	security find-certificate -c "$identity" -a -p "$keychain" > "$pem_block/all.pem" 2>/dev/null || true
	awk -v dir="$pem_block" '
		/-----BEGIN CERTIFICATE-----/ { n++; file = dir "/cert-" n ".pem" }
		n { print > file }
	' "$pem_block/all.pem"
	for pem_file in "$pem_block"/cert-*.pem; do
		[ -s "$pem_file" ] || continue
		sha1="$(cert_sha1 "$pem_file")"
		[ -n "$sha1" ] && echo "$sha1"
	done | sort -u
	rm -rf "$pem_block"
}

verify_identity() {
	security find-identity -v -p codesigning "$keychain" | grep -Fq "\"$identity\"" ||
		die "未在 $keychain 找到有效的签名身份“${identity}”。请先执行 bash scripts/macos-signing.sh --import"
}

import_identity() {
	[ -f "$p12" ] || die "缺少 ${p12}；签名材料应随仓库提供"
	[ -f "$cer" ] || die "缺少 ${cer}；签名材料应随仓库提供"

	local repo_sha1 existing_sha1 count
	repo_sha1="$(cert_sha1 "$cer")"
	[ "$repo_sha1" = "$expected_sha1" ] ||
		die "$cer 的指纹是 ${repo_sha1}，期望 ${expected_sha1}；证书可能被替换，请确认后再改脚本里的 expected_sha1"

	existing_sha1="$(keychain_cert_sha1s)"
	count="$(printf '%s\n' "$existing_sha1" | sed '/^$/d' | wc -l | tr -d ' ')"

	if [ "$count" -gt 0 ]; then
		if [ "$count" -gt 1 ]; then
			echo "$existing_sha1" >&2
			die "钥匙串里有多个“${identity}”证书（见上），codesign 可能选错。请先执行 bash scripts/macos-signing.sh --remove"
		fi
		if [ "$existing_sha1" = "$repo_sha1" ]; then
			note "签名身份已存在且指纹一致（${repo_sha1}）"
			verify_identity
			note "无需重复导入"
			return 0
		fi
		die "本机已有同名但指纹不同的证书（${existing_sha1}）。请先执行 bash scripts/macos-signing.sh --remove，避免 TCC 授权错乱"
	fi

	note "导入 $p12"
	security import "$p12" -k "$keychain" -f pkcs12 -P "$p12_password" \
		-T /usr/bin/codesign -T /usr/bin/security

	note "信任证书（如弹出授权框请允许）"
	security add-trusted-cert -p codeSign -r trustRoot -k "$keychain" "$cer"

	verify_identity
	echo
	echo "已导入签名身份“${identity}”"
	echo "  SHA-1  $repo_sha1"
	echo "  之后 go tool mygo build 会自动用它签名。"
}

export_identity() {
	local force="$1" export_pass tmp exported_sha1
	verify_identity

	[ -f "$p12" ] && [ "$force" -ne 1 ] &&
		die "$p12 已存在；确认要覆盖请加 --force"

	export_pass="$(/usr/bin/openssl rand -hex 16)"
	tmp="$(mktemp -d "${TMPDIR:-/tmp}/toolbox-export.XXXXXX")"
	# 用双引号让 $tmp 在设置 trap 时就展开：函数返回后 local 变量已不可见。
	trap "rm -rf '$tmp'" EXIT

	note "导出钥匙串身份（可能弹出授权框，请允许 security 访问私钥）"
	# security export 不支持按名称过滤，会导出全部身份；下面按公钥哈希挑出目标私钥。
	security export -t identities -f pkcs12 -k "$keychain" -P "$export_pass" -o "$tmp/all.p12" >/dev/null

	security find-certificate -c "$identity" -p "$keychain" > "$tmp/cert.pem"
	[ -s "$tmp/cert.pem" ] || die "未找到证书“${identity}”"

	local cert_pub
	cert_pub="$(/usr/bin/openssl x509 -in "$tmp/cert.pem" -pubkey -noout |
		/usr/bin/openssl pkey -pubin -outform DER | shasum -a 256 | awk '{print $1}')"

	/usr/bin/openssl pkcs12 -in "$tmp/all.p12" -passin "pass:$export_pass" \
		-nocerts -nodes -out "$tmp/keys.pem" 2>/dev/null
	awk -v dir="$tmp" '
		/-----BEGIN (RSA )?PRIVATE KEY-----/ { n++ }
		n { print > (dir "/key-" n ".pem") }
	' "$tmp/keys.pem"

	local key key_pub found=""
	for key in "$tmp"/key-*.pem; do
		[ -s "$key" ] || continue
		key_pub="$(/usr/bin/openssl pkey -in "$key" -pubout -outform DER 2>/dev/null |
			shasum -a 256 | awk '{print $1}')"
		if [ "$key_pub" = "$cert_pub" ]; then
			found="$key"
			break
		fi
	done
	[ -n "$found" ] || die "未能从导出的身份中找到与“${identity}”配对的私钥"

	mkdir -p "$material"
	/usr/bin/openssl pkcs12 -export -descert -inkey "$found" -in "$tmp/cert.pem" \
		-name "$identity" -passout "pass:$p12_password" -out "$p12"
	cp "$tmp/cert.pem" "$cer"

	exported_sha1="$(cert_sha1 "$cer")"
	echo
	echo "已导出签名身份到 $material"
	echo "  SHA-1  $exported_sha1"
	echo "  口令   ${p12_password}（见 signing/README.md）"
	if [ "$exported_sha1" != "$expected_sha1" ]; then
		echo "⚠️  指纹与脚本里的 expected_sha1 不一致：如果这是有意换证书，请同步更新脚本和 README。"
	fi
}

verify_all() {
	note "检查钥匙串身份"
	verify_identity
	security find-identity -v -p codesigning "$keychain" | grep -F "\"$identity\"" || true

	local repo_sha1 keychain_sha1 expiry
	repo_sha1="$(cert_sha1 "$cer" 2>/dev/null || true)"
	keychain_sha1="$(security find-certificate -c "$identity" -p "$keychain" 2>/dev/null |
		/usr/bin/openssl x509 -noout -fingerprint -sha1 2>/dev/null | sed 's/.*=//' | normalize_sha1 || true)"

	[ "$repo_sha1" = "$expected_sha1" ] || die "仓库证书指纹 $repo_sha1 != $expected_sha1"
	[ "$keychain_sha1" = "$expected_sha1" ] || die "本机证书指纹 $keychain_sha1 != $expected_sha1"

	note "证书指纹一致：$repo_sha1"
	expiry="$(cert_expiry "$cer")"
	echo "  有效期至 $expiry"
	if ! /usr/bin/openssl x509 -in "$cer" -noout -checkend 2592000 >/dev/null 2>&1; then
		echo "⚠️  证书将在 30 天内过期；换证书会让所有用户重新授权一次。"
	fi

	local app dr
	for app in build/darwin-*/toolbox.app; do
		[ -d "$app" ] || continue
		dr="$(codesign -d -r- "$app" 2>&1 | tail -1)"
		echo "  $app"
		echo "    $dr"
		case "$dr" in
		*cdhash*) echo "⚠️  $app 仍是 ad-hoc 签名" ;;
		*"certificate leaf = H\"$expected_sha1\""*) echo "    ✓ DR 使用固定证书" ;;
		*) echo "⚠️  未识别的 DR" ;;
		esac
	done
}

remove_identity() {
	local sha1
	sha1="$(cert_sha1 "$cer" 2>/dev/null || true)"

	note "删除本机签名身份“${identity}”"
	security delete-identity -c "$identity" -t "$keychain" >/dev/null 2>&1 || true
	if [ -n "$sha1" ]; then
		security delete-certificate -Z "$sha1" -t "$keychain" >/dev/null 2>&1 || true
	fi
	security remove-trusted-cert -d "$cer" >/dev/null 2>&1 ||
		security remove-trusted-cert "$cer" >/dev/null 2>&1 || true
	security delete-certificate -c "$identity" -t "$keychain" >/dev/null 2>&1 || true

	echo "已删除。仓库里的 signing/ 文件保持不变；重新执行 --import 会恢复同一张证书（授权不受影响）。"
}

action="import"
force=0
for arg in "$@"; do
	case "$arg" in
	--import) action="import" ;;
	--export) action="export" ;;
	--verify) action="verify" ;;
	--remove) action="remove" ;;
	--force) force=1 ;;
	-h | --help)
		sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		die "未知参数：$arg"
		;;
	esac
done

case "$action" in
import) import_identity ;;
export) export_identity "$force" ;;
verify) verify_all ;;
remove) remove_identity ;;
esac
