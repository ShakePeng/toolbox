#!/usr/bin/env bash
# 创建/删除一个本地固定代码签名证书，让 app 在多轮构建间保持稳定签名身份，
# 这样 macOS 的“屏幕录制/辅助功能”授权不会因为重新打包而反复失效。
#
#   bash scripts/macos-signing.sh            # 创建并信任（首次）
#   bash scripts/macos-signing.sh --remove   # 删除证书
#
# 创建完成后用固定身份打包：
#   go tool mygo build -sign "$TOOLBOX_SIGNING_IDENTITY"
# 或把 macos.signingIdentity 写进 mygo.json（本项目已设置），直接 go tool mygo build。

set -euo pipefail
umask 077

identity="${TOOLBOX_SIGNING_IDENTITY:-Toolbox Local Dev}"
keychain="${TOOLBOX_SIGNING_KEYCHAIN:-$HOME/Library/Keychains/login.keychain-db}"

has_identity() {
  security find-identity -v -p codesigning "$keychain" 2>/dev/null |
    grep -Fq "\"$identity\""
}

if [[ "${1:-}" == "--remove" ]]; then
  if has_identity; then
    security delete-identity -c "$identity" -t "$keychain"
    echo "已删除本地签名身份“${identity}”。"
  elif security find-certificate -c "$identity" "$keychain" >/dev/null 2>&1; then
    security delete-certificate -c "$identity" -t "$keychain"
    echo "已删除不完整的本地签名证书“${identity}”。"
  else
    echo "本地签名身份“${identity}”不存在，无需删除。"
  fi
  exit 0
fi

if has_identity; then
  echo "本地签名身份“${identity}”已存在。"
  exit 0
fi

temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/toolbox-signing.XXXXXX")"
trap 'rm -rf "$temporary_dir"' EXIT

certificate="$temporary_dir/certificate.pem"
private_key="$temporary_dir/private-key.pem"
bundle="$temporary_dir/identity.p12"
bundle_password="$(/usr/bin/openssl rand -hex 24)"

if security find-certificate -c "$identity" "$keychain" >/dev/null 2>&1; then
  security find-certificate -c "$identity" -p "$keychain" >"$certificate"
  security add-trusted-cert -p codeSign -r trustRoot -k "$keychain" "$certificate"
  if has_identity; then
    echo "已修复并信任本地签名身份“${identity}”。"
    exit 0
  fi
  echo "证书“${identity}”已存在，但缺少可用的私钥或信任设置。" >&2
  echo "请先执行 bash scripts/macos-signing.sh --remove，再重新初始化。" >&2
  exit 1
fi

/usr/bin/openssl req \
  -x509 \
  -newkey rsa:2048 \
  -sha256 \
  -days 3650 \
  -nodes \
  -keyout "$private_key" \
  -out "$certificate" \
  -subj "/CN=$identity" \
  -addext "basicConstraints=critical,CA:FALSE" \
  -addext "keyUsage=critical,digitalSignature" \
  -addext "extendedKeyUsage=critical,codeSigning"

/usr/bin/openssl pkcs12 \
  -export \
  -descert \
  -inkey "$private_key" \
  -in "$certificate" \
  -name "$identity" \
  -out "$bundle" \
  -passout "pass:$bundle_password"

security import "$bundle" \
  -k "$keychain" \
  -P "$bundle_password" \
  -T /usr/bin/codesign \
  -T /usr/bin/security

security add-trusted-cert -p codeSign -r trustRoot -k "$keychain" "$certificate"

if ! has_identity; then
  echo "证书已导入，但系统仍未将其识别为可用的代码签名身份。" >&2
  exit 1
fi

echo "已创建并信任本地签名身份“${identity}”。"
echo "之后 go tool mygo build 会自动用它签名，无需重新授权。"
