# 签名材料

这里的证书用于给 toolbox 做 macOS 代码签名。它的作用是让屏幕录制/辅助功能授权（TCC）在版本更新后继续有效。

## 为什么需要固定证书

macOS 的 TCC 授权会记录应用的 **designated requirement**：

- ad-hoc 签名（`codesign -s -`）的要求是 `cdhash H"..."`，每次重新构建都会变，所以每次更新后都要重新授权。
- 固定证书签名的要求是 `identifier "com.example.toolbox" and certificate leaf = H"..."`。只要证书和 bundle id 不变，更新后就沿用原来的授权。

`scripts/dev.sh` 用同一张证书签开发包（`com.example.toolbox.dev`），开发包授权一次后也不会因代码重建而重置。

## 证书

| 项目 | 值 |
| --- | --- |
| 名称 | `Toolbox Local Dev` |
| 类型 | 自签名，RSA 2048，EKU Code Signing |
| SHA-1 | `1BDA019447623096DBAF37354A7741F69151D8CB` |
| SHA-256 | `58BAE2DE119692ECA50CF2CCB1BFCA4E477A99D6BB5429DF715C97A990492A50` |
| 有效期 | 2026-11-09 至 2036-11-06 |
| p12 口令 | `toolbox-local-dev` |

## 文件

- `toolbox-signing.p12`：证书 + 私钥，**明文提交**。
- `toolbox-signing.cer`：公开证书，CI 用它做信任设置。

> ⚠️ 仓库是公开的，任何拿到这个 p12 的人都能签发与 toolbox 相同签名身份的应用。这是个人项目下有意接受的取舍；如果以后要改变，换一张新证书即可，但所有用户会重新授权一次。

## 本机初始化

```bash
bash scripts/macos-signing.sh            # 导入并信任
bash scripts/macos-signing.sh --verify   # 校验指纹、身份和已构建 app 的 DR
```

如果弹出钥匙串授权框，选择允许。

## 换一台电脑

clone 仓库后执行上面的 `--import`/`--verify` 即可，不需要重新生成证书。**不要重新生成证书**：新证书是不同的签名身份，会让所有人的授权失效。

## 导出（轮换或备份）

```bash
bash scripts/macos-signing.sh --export
```

`security export` 会导出钥匙串里的全部身份，脚本会按公钥哈希挑出 `Toolbox Local Dev` 的私钥，重新打包成只含目标身份的 p12。加 `--force` 覆盖已有文件。

导出后确认 `toolbox-signing.cer` 的 SHA-1 仍是 `1BDA0194...D8CB`。

## CI

`.github/workflows/release.yml` 会：

1. 校验 `toolbox-signing.cer` 的 SHA-1；
2. 把 p12 导入临时 keychain 并信任证书；
3. 用 `-sign "Toolbox Local Dev"` 构建；
4. 校验产物的 DR 是证书型而不是 `cdhash`。

## 已知限制

- **Sequoia 屏幕录制再确认**：macOS 15 对直接抓屏有约 30 天一次的“Allow For One Month”确认，记录在 `~/Library/Group Containers/group.com.apple.replayd/ScreenCaptureApprovals.plist`，与签名无关。想彻底避免需要改用 `SCContentSharingPicker`。
- **新机器 Gatekeeper**：自签名证书不能公证，首次打开需在“系统设置 → 隐私与安全性”里选择“仍要打开”。
- **证书过期**：2036-11-06 到期。换证书会导致所有用户重新授权一次。
- **升级到固定签名的第一个版本**（1.0.4）：需要最后一次重新授权；如果系统设置里开关仍开着但权限不生效，用减号移除旧的 toolbox 条目，再启动应用重新授权。
