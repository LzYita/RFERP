# 杀软误报修复流程（AV false positive remediation）

**状态**：可执行
**触发事件**：2026-11-06 协作者机器验证中发现 Kaspersky 删除构建产物
**影响面**：发布工具链（阻断级）、局域网交付包（观察级）
**关联**：`docs/plans/lan-v1.md` R15 / R31 / R32；协作者《杀毒告警排查记录》

---

## 0. 一页结论

| 项 | 结论 |
|---|---|
| 根因 | **未 strip 的 Go 二进制**（符号/调试信息）触发云启发式判定，不是业务代码、不是缺证书 |
| 实测判据 | 未 strip：6,349,824 B / 6,201 个符号 → 被判 `VHO:Trojan.Win64.Convagent.gen` 并删除；`-trimpath -ldflags "-s -w"`：4,342,784 B / 0 个符号 → 通过 |
| 已受累 | `cmd/signmanifest`（发布签名工具，**会让 releases.json 生成失败**） |
| 未受累 | `RFERP-server.exe`、`RFERP.exe`、`rferp-license.exe`（均已 strip，Kaspersky 报告零检测） |
| 修复主体 | ① 机器排除项止血 ② 所有 Go 二进制强制 strip 且有机械校验 ③ 交付 SOP 前置杀软信任区 ④ 误报申诉 |
| 不买证书 | 证书解决"发布者未知"，**不解决**启发式误报；先不买，用误报率数据决策 |
| 修复周期 | P0 当天 30 分钟；P1 1–2 天；P2 试点前完成；P3 每次发版执行 |

**一句话**：把"必须 strip"从**靠人记**变成**靠脚本拦**，同时让客户的杀软不再把正常文件当病毒。

---

## 1. 根因与判据

### 1.1 三类判定名的区别（决定处置方式）

| 前缀 | 含义 | 本例 | 处置 |
|---|---|---|---|
| `not-a-virus:` | 合法但可被滥用（网络工具、抓包驱动） | FRP、WinDivert | 来源核验 + 目录排除；**不删不留后门** |
| `PDM:` | 行为分析（看进程做了什么） | `free_space.ps1`、ZCode | 复核具体动作；**不整体关闭行为防护** |
| `VHO:` | 云保护 + 启发式（看文件特征与信誉） | `signmanifest` | **改变二进制特征**（strip）+ 申诉 |

`VHO` 判定是**时变的**：同一份源码 2026-10-31 无记录、11-01 起被删。所以"昨天能跑"不能作为判断依据。

### 1.2 strip 判据（已在本机验证，用于脚本实现）

```
$ go build -o plain.exe ./cmd/signmanifest
$ go build -trimpath -ldflags "-s -w" -o stripped.exe ./cmd/signmanifest

$ ls -l
plain.exe      6,349,824      stripped.exe   4,342,784

$ go version -m plain.exe        → exit 0（能读，但**不记录** -ldflags，不能用它判 strip）
$ go version -m stripped.exe     → exit 0（含 build -trimpath=true）

$ go tool nm plain.exe    | wc -l → 6201   （未 strip）
$ go tool nm stripped.exe | wc -l → 0      （已 strip，exit 1）
```

**结论**：判据必须是「`go version -m` 成功（是 Go 二进制）+ `go tool nm` 符号数为 0（已 strip）」。只用 `go tool nm` 会把非 Go 文件误判成"已 strip"。

### 1.3 发布脚本现状

| 产物 | 构建方式 | strip |
|---|---|---|
| `RFERP-server.exe` | `build-server-package.ps1:52`：`go build -trimpath -tags production -ldflags "-s -w -X main.version=…"` | ✅ |
| `RFERP.exe` | `build-client-installer.ps1:51`：`go build -trimpath -tags "desktop,production" -ldflags "-s -w -H windowsgui -X main.version=…"` | ✅ |
| `signmanifest.exe` | **仓库外脚本/手工命令**，无固定参数 | ❌ 无保障 |
| `rferp-license.exe` | **仓库外**，无固定参数 | ❌ 无保障 |
| `WinSW.exe` | 构建时下载（v2.12.0），仅校验 MZ 头与 >1MB | N/A（第三方） |

这就是为什么主二进制干净、`signmanifest` 中招。

---

## 2. 影响面分级

| 级别 | 对象 | 后果 |
|---|---|---|
| **阻断** | 发布机上的 `cmd/signmanifest` 构建/测试 | 文件被删或拒绝访问 → `releases.json` 生成失败/不完整 → 自动更新发出坏清单 |
| **阻断** | 任何未 strip 的新二进制（未来新增的 cmd） | 同上，且无校验会在客户现场才暴露 |
| **观察** | 客户机 `install-host.ps1`（注册服务/改防火墙/改 ACL） | PDM 行为分析可能弹窗或拦截（同类脚本已被判木马） |
| **观察** | 交付包内 `WinSW.exe`（第三方未签名） | 首次运行可能被拦；当前只校验 MZ 头，存在供应链风险 |
| **观察** | 客户机 `RFERP-server.exe` 长驻 + 写 DB + `VACUUM INTO` | 行为监控可能误判；扫描期锁库导致备份偶发 BUSY |
| **不受影响** | CI（GitHub Actions） | 无卡巴斯基 → **这类问题在 CI 里永远不可见** |

---

## 3. 修复流程

### P0 · 现场止血（当天，约 30 分钟）

**目标**：协作者机器恢复可构建、可测试、可发版。

**P0-1 加目录排除（5 分钟）**

卡巴斯基 → 设置 → 安全 → 排除项 → 添加：

```
C:\Users\asus\code\RFERP
%TEMP%\go-build*
```

> 只加这两个路径。**不要**关闭文件监控或行为防护——那等于把整台机器暴露给真实威胁。

**P0-2 固定 signmanifest 构建命令**

任何构建/测试都必须带 strip：

```bash
# 构建
go build -trimpath -ldflags "-s -w" -o ./dist/signmanifest.exe ./cmd/signmanifest

# 直接用（不发版时）
go run -trimpath -ldflags "-s -w" ./cmd/signmanifest \
  -zip ./dist/RFERP-2.2.0.zip -url <下载地址> -version 2.2.0 -out ./dist/releases.json

# 测试
go test -trimpath -ldflags "-s -w" ./cmd/signmanifest
```

验收：`go tool nm ./dist/signmanifest.exe | wc -l` 输出 `0`。

> `-trimpath` 单独使用**无效**（实测仍被删），必须 `-s -w`。

**P0-3 提交误报（15 分钟）**

<https://opentip.kaspersky.com/> → 上传 `signmanifest.exe` → 填写：

```
判定名：VHO:Trojan.Win64.Convagent.gen
说明：Go 编译的内部发布工具（Ed25519 清单签名），非恶意。
      同一源码加 -ldflags "-s -w" 后不再命中，说明命中的是调试/符号信息。
```

若同时向 Microsoft 报误报：<https://www.microsoft.com/en-us/wdsi/filesubmission>。

**P0-4 检查残留**

```bash
ls -la dist/ | grep -i sig          # 清掉 _sigcheck*.exe 等临时产物
git status --short dist/            # 确认没有被跟踪的二进制（.gitignore 已含 *.exe、dist/）
```

**P0 验收**

- [ ] 排除项已生效（重新构建后文件 60 秒不消失）
- [ ] `signmanifest` 构建产物符号数为 0
- [ ] 误报已提交，并记录提交编号
- [ ] `dist/` 无临时签名产物

**失败回退**：若排除项加不上（策略锁定），退到只跑 CI/另一台机器发版；**不要**为了本机而改仓库代码或关闭防护。

---

### P1 · 仓库加固（1–2 天）

**目标**：把"必须 strip"从约定变成机械校验，堵住"新加二进制忘了带 `-s -w`"。

**P1-1 落地发布前自检脚本（已完成）**

新增 `deploy/windows/verify-release.ps1`（UTF-8 with BOM，PS 5.1 已验证可解析）：

```powershell
pwsh deploy/windows/verify-release.ps1 -Path dist
```

行为：

| 情形 | 输出 | 退出码 |
|---|---|---|
| 全部 Go 二进制已 strip | 表格 + "全部已 strip" | 0 |
| 存在未 strip | 表格 + 红字禁止发布 | **1** |
| 含第三方 exe（如 WinSW） | 标"第三方/未知"，提示单独锁定哈希 | 0 |
| 路径不存在/无 exe/无 go | 明确报错 | 2 |

已在本机用三组样本验证：混合目录 exit 1；纯 stripped exit 0；`-AllowUnstripped` 放行并告警。

**P1-2 修 `build-server-package.ps1`：WinSW 哈希固定（R32）**

现状只校验 `MZ` 头与大小，而 URL 走的是第三方加速器 `ghfast.top`：

```powershell
# 现状（build-server-package.ps1:73-76）
$bytes = [System.IO.File]::ReadAllBytes($winsw)
if ($bytes.Length -lt 1MB -or $bytes[0] -ne 0x4D -or $bytes[1] -ne 0x5A) {
    throw "WinSW.exe 不是有效的 Windows 可执行文件…"
}
```

改为（参数 + 校验）：

```powershell
param(
    ...
    [string]$WinSWUrl = "https://ghfast.top/…/v2.12.0/WinSW-x64.exe",
    [string]$WinSWSha256 = "<把官方 v2.12.0 x64 的 SHA256 填在这里>"
)
...
$hash = (Get-FileHash -Algorithm SHA256 -Path $winsw).Hash
if ($WinSWSha256 -and $hash -ne $WinSWSha256.ToUpperInvariant()) {
    Remove-Item $winsw -Force
    throw "WinSW.exe 哈希不匹配（实际 $hash）。加速器可能被劫持或版本被替换。"
}
```

获取基准哈希的方式：从 `https://github.com/winsw/winsw/releases/tag/v2.12.0` 下载一次，本地算 `SHA256`，写进脚本并记入 `docs/lan-deployment.md`。

**P1-3 在 `cmd/signmanifest` 顶部加构建说明**

`cmd/signmanifest/main.go` 包注释补一段（避免下一个人再踩）：

```go
// 构建注意：本包的产物在本机安全软件（Kaspersky）上被云启发式判为
// VHO:Trojan.Win64.Convagent.gen 并删除，命中点在符号/调试信息。
// 必须带 strip 构建：
//   go build -trimpath -ldflags "-s -w" -o dist/signmanifest.exe ./cmd/signmanifest
// 发布前可用 deploy/windows/verify-release.ps1 校验。
// 详见 docs/plans/av-false-positive-remediation.md。
```

**P1-4 对齐 Go 工具链版本**

本机 `go1.26.5`，CI/发布基线 `go1.26.10`。不同工具链产出的二进制**特征不同**，会让"本机能过、发版被删"这类问题更难复现。

- 发布机与 CI 统一到 `1.26.10`；
- 记录在 `CONTRIBUTING.md` 的构建环境表里。

**P1-5（可选）清理无效构建参数**

`build-server-package.ps1` 传了 `-tags production`，但仓库里**没有任何** `//go:build ... production` 文件。它当前是空操作，容易让人误以为有生产分支。建议删除，或在 CI 加一条断言说明其用途。

**P1 验收**

- [ ] `verify-release.ps1` 在真实 `dist/` 上跑通（先用当前的未 strip 产物验证它会拦下）
- [ ] `build-server-package.ps1` 带哈希参数构建一次成功，故意改错哈希能拦下
- [ ] `cmd/signmanifest` 注释已加
- [ ] 发布机 Go 版本 = 1.26.10

---

### P2 · 交付与客户侧（试点前完成）

**目标**：客户装得上、用得稳，而且出了问题有人能自己解决。

**P2-1 交付 SOP 前置杀软信任区**

`docs/lan-v1.md` §9 第 4 步已改为：**先在杀软信任区加入数据目录 + 安装目录** → 安装器 → 服务 → 防火墙 → 初始化令牌。

客户侧要加的路径：

```
安装目录：C:\Program Files\RFERP（或实际安装路径）
数据目录：D:\RFERP数据（或实际数据路径）
```

三家指引（写进 `docs/lan-deployment.md`）：

| 产品 | 路径 | 操作 |
|---|---|---|
| Windows Defender | 设置 → 隐私和安全性 → 病毒和威胁防护 → 排除项 | 添加文件夹 ×2 |
| Kaspersky | 设置 → 安全 → 排除项 | 按掩码/文件夹添加 ×2 |
| 360 / 火绒 / 腾讯管家 | 设置 → 信任区/白名单 | 按目录添加 ×2 |

**P2-2 交付物增加两项**

1. `SHA256SUMS.txt`：发布包里每个文件的哈希，`build-server-package.ps1` 打包时生成；
2. `首次运行被拦截怎么办.md`（一页纸），内容框架：

```markdown
## 症状
- 双击 exe 出现蓝色「Windows 已保护你的电脑」
- 杀软弹窗提示「发现风险程序」/ 文件消失
- 安装脚本提示「无法加载，因为在此系统上禁止运行脚本」

## 原因
本软件是内部工具，未购买商业代码签名证书，Windows 与杀软会按"未知发布者"提示。
这是**提示**，不是病毒确认。

## 处理
1. SmartScreen 蓝框：点「更多信息」→「仍要运行」
2. 文件被标记为"来自网络"：右键文件 → 属性 → 勾选「解除锁定」→ 确定
3. 杀软提示：选择「允许」/「添加信任」；已在信任区的不会再提示
4. PowerShell 禁止运行脚本：用管理员 PowerShell 执行
   `Set-ExecutionPolicy -Scope Process Bypass -Force` 后再运行安装脚本
5. 核对文件哈希：`certutil -hashfile RFERP-server.exe SHA256`，与 SHA256SUMS.txt 比对
6. 仍有问题：把杀软名称+版本+截图发给支持
```

**P2-3 实测两个未验证项**

| 项 | 怎么测 | 通过标准 |
|---|---|---|
| `WinSW.exe` 首次运行 | 干净虚拟机（装 Defender）解压交付包 → 运行 `install-host.ps1` | 无拦截，或可按 P2-2 指引放行 |
| `install-host.ps1` 行为拦截 | 同上，观察 PDM/行为防护是否弹窗 | 最多一次"是否允许以管理员运行" |

若 `install-host.ps1` 被 PDM 判定，**不要**建议客户关闭行为防护；改为把脚本拆细、或在文档里给出逐步手工替代（手动 `sc create` + `netsh` 命令）。

**P2 验收**

- [ ] 三家杀软信任区指引已写进部署文档
- [ ] 发布包含 `SHA256SUMS.txt`，哈希与文件一致
- [ ] 一页纸已附在交付包内
- [ ] 干净虚拟机上 WinSW + install-host.ps1 实测记录

---

### P3 · 发布流程闸门（每次发版）

**目标**：让"发出去的东西一定 strip"不依赖记忆。

**发版检查单（追加到现有流程）**

```bash
# 1. 构建（脚本已内置 strip）
pwsh deploy/windows/build-server-package.ps1 -Version 2.2.0 -PublicKeyFile <pub>
pwsh deploy/windows/build-client-installer.ps1 -Version 2.2.0

# 2. 自检：必须 exit 0
pwsh deploy/windows/verify-release.ps1 -Path dist

# 3. 生成哈希清单
#    （build-server-package.ps1 后续内置；临时可用：）
cd dist && sha256sum *.zip *.exe > SHA256SUMS.txt

# 4. 签名清单（必须 strip 后再跑）
go run -trimpath -ldflags "-s -w" ./cmd/signmanifest \
  -zip dist/RFERP-2.2.0.zip -url <url> -version 2.2.0 -out dist/releases.json

# 5. 本机跑一次：解压服务端包 → install-host.ps1 → /readyz 200 → 卸载
```

**闸门规则**：`verify-release.ps1` 退出码非 0 → **禁止发布**，回到第 1 步重构建。

**CI 侧（可选但建议）**

在 `.github/workflows/ci.yml` 的 Windows 作业里加一步：

```yaml
- name: Verify release binaries are stripped
  shell: pwsh
  run: |
    go build -trimpath -ldflags "-s -w" -o verify-strip.exe ./cmd/verify-strip  # 或直接复用 PS 脚本
    ./deploy/windows/verify-release.ps1 -Path <构建产物目录>
```

> CI 无法复现"被杀软删除"（没有卡巴斯基），但**能**复现"忘了 strip"。这是 CI 唯一可靠的防线。

---

### P4 · 复发监测与升级路径

**目标**：误报是时变的，要能被发现，且知道什么时候该花钱。

**P4-1 监测**

| 信号 | 采集方式 | 频率 |
|---|---|---|
| 客户报"文件消失/被拦" | 支持工单分类为 `av-blocked` | 实时 |
| 发版后自机冒烟失败 | 每版安装一次（P3 第 5 步） | 每版 |
| 新二进制加入仓库 | `verify-release.ps1` 自动覆盖 | 每次发版 |

**P4-2 买证书的决策条件**

不要凭感觉买。用这三条判断：

1. **同一客户**在 30 天内被拦 ≥2 次 → 买 OV 证书；
2. 试点期累计 `av-blocked` 工单 ≥ 客户数 × 1 → 买；
3. 客户明确要求"安装不能有安全提示" → 买。

参考成本（以官网为准）：OV 约 ¥600–1500/年；EV 约 ¥1500–3000/年；Azure Trusted Signing 约 $10/月（需组织验证，中国大陆可用性需确认）。

> 买证书**不能**避免启发式误报，只是把"未知发布者"变成具体发布者，并让 SmartScreen 有信誉。买之前先确认误报已经通过 strip 解决。

**P4-3 已知不接受的做法**

| 做法 | 为什么不做 |
|---|---|
| 关闭杀软/Defender | 把客户整机暴露给真实威胁 |
| 把 `powershell.exe` 加白 | 等于关掉一大类行为防护 |
| 把 `%TEMP%` 整目录加白 | 临时目录是恶意软件最常写的位置 |
| 改仓库代码绕过判定 | 命中点不在业务代码，改代码无效且污染仓库 |
| 提交构建产物进仓库 | 二进制会跟着版本走，且无法保证 strip |
| 用自签证书签名 | 客户机不信任该根证书，装根证书本身会被拦 |

---

## 4. 责任与时间表

| 阶段 | 谁 | 何时 | 交付 |
|---|---|---|---|
| P0 现场止血 | 协作者（本机） | 当天 | 排除项 + strip 构建 + 误报编号 |
| P1 仓库加固 | 我 | 1–2 天 | `verify-release.ps1`（已完成）、WinSW 哈希、注释、Go 版本 |
| P2 交付侧 | 我 + 实施 | 试点前 | 信任区指引、SHA256SUMS、一页纸、VM 实测 |
| P3 发版闸门 | 发版人 | 每次发版 | 五项检查单 |
| P4 监测 | 支持 | 持续 | `av-blocked` 工单与决策记录 |

---

## 5. 回滚

| 场景 | 回滚动作 |
|---|---|
| 排除项导致真实威胁未被拦 | 立即移除排除项，对安装/数据目录做一次全盘扫描 |
| `verify-release.ps1` 误报（把好二进制判为未 strip） | 用 `-AllowUnstripped` 放行并记录，同时修脚本判据；**不要**改发布产物 |
| WinSW 哈希校验导致构建失败 | 从官方 Release 重新下载并核对 `SHA256SUMS.txt`；若官方哈希确实变了（上游换了包），更新脚本常量并记录 |
| 加了 strip 后功能异常 | 暂不 strip 并停止发版，先定位（strip 只影响调试信息，理论上不影响运行；异常通常是别的原因） |

---

## 6. 明确不做

- 不为这件事改业务代码。
- 不因为杀软而关闭客户或自己的防护。
- 不在本阶段买代码签名证书（等 P4-2 的数据）。
- 不把 `signmanifest`/`rferp-license` 交付给客户（内部工具）。
- 不把 `jitter-proxy.py`、`smoke-lan.py` 打进交付包（当前已正确排除，保持）。

---

## 7. 验收清单

**P0**

- [ ] 协作者机器排除项已加，`signmanifest` 构建后 60 秒不消失
- [ ] `go tool nm dist/signmanifest.exe | wc -l` = 0
- [ ] 误报已提交，编号记录在案

**P1**

- [ ] `deploy/windows/verify-release.ps1` 存在、UTF-8 BOM、PS 5.1 可解析
- [ ] 三组用例通过：混合 exit 1 / 纯 stripped exit 0 / `-AllowUnstripped` exit 0
- [ ] `build-server-package.ps1` 支持 `-WinSWSha256`，错哈希能拦
- [ ] `cmd/signmanifest/main.go` 构建说明已加
- [ ] 发布机 Go = 1.26.10

**P2**

- [ ] 部署文档含 Defender/Kaspersky/360 三家信任区指引
- [ ] 交付包含 `SHA256SUMS.txt` 与《首次运行被拦截怎么办》
- [ ] 干净 VM 实测 WinSW + `install-host.ps1` 无阻断

**P3**

- [ ] 发版检查单五项已并入现有流程
- [ ] 故意产出一个未 strip 的二进制，`verify-release.ps1` 能拦下并 exit 1

---

## 8. 附：关键命令速查

```bash
# 判断是否 strip
go version -m dist/RFERP-server.exe              # exit 0 = 是 Go 二进制
go tool nm dist/RFERP-server.exe | wc -l         # 0 = 已 strip

# 正确构建（内部工具）
go build -trimpath -ldflags "-s -w" -o dist/signmanifest.exe ./cmd/signmanifest
go run   -trimpath -ldflags "-s -w" ./cmd/signmanifest -zip … -out releases.json

# 发布前自检
pwsh deploy/windows/verify-release.ps1 -Path dist

# 生成哈希清单
cd dist && sha256sum *.zip *.exe > SHA256SUMS.txt

# 拉取卡巴斯基报告（协作者机器）
cd "/c/Program Files (x86)/Kaspersky Lab/Kaspersky 21.25"
./avp.com REPORT FM  "/RA:C:\Users\asus\AppData\Local\Temp\kav-fm.txt"
./avp.com REPORT SW2 "/RA:C:\Users\asus\AppData\Local\Temp\kav-sw2.txt"

# 复现（未 strip 应被删）
go build -o ./dist/_repro.exe ./cmd/signmanifest
sha256sum ./dist/_repro.exe && sleep 45 && ls -la ./dist/_repro.exe   # 预期：不存在
```
