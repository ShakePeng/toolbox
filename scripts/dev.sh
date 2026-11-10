#!/usr/bin/env bash
# 以固定签名身份运行 mygo dev。
#
# `mygo dev` 默认用 ad-hoc 签名，且不读取 mygo.json 的 macos.signingIdentity，
# 每次重建的 cdhash 都不同，开发包的屏幕录制/辅助功能授权会被反复重置。
# 这里显式传 -sign，开发包（bundle id 固定为 com.example.toolbox.dev）授权
# 一次后，代码重建不再影响授权。
#
# 注意：开发包与正式包是两个 TCC 应用（MyGo 固定给 dev 加 .dev 后缀）。
# 想在开发时复用正式包的授权，请用 `go tool mygo build` 构建并运行打包版。

set -euo pipefail
cd "$(dirname "$0")/.."

identity="${TOOLBOX_SIGNING_IDENTITY:-Toolbox Local Dev}"

if ! security find-identity -v -p codesigning | grep -Fq "\"$identity\""; then
	echo "未找到签名身份“${identity}”。请先执行 bash scripts/macos-signing.sh" >&2
	exit 1
fi

exec go tool mygo dev -sign "$identity" "$@"
