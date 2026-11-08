<#
.SYNOPSIS
    发布前二进制自检：确认每个 exe 都已 strip（-s -w），并列出第三方二进制。

.DESCRIPTION
    存在的理由：2026-11-06 协作者机器上，未 strip 的 `cmd/signmanifest` 构建产物
    被 Kaspersky 判为 `VHO:Trojan.Win64.Convagent.gen` 并直接删除；同一份源码
    加 `-ldflags "-s -w"` 后通过。根因在调试/符号信息，不在业务代码。

    发布脚本目前靠手写 `-ldflags` 维持 strip，没有任何机械校验：新加一个二进制
    忘了带 `-s -w`，就要等客户装杀软之后才发现。本脚本把这条判据固化。

    判据（已验证，见 docs/plans/av-false-positive-remediation.md）：
      - `go version -m <exe>` 退出码 0  → 确认为 Go 二进制（strip 后仍可读）；
      - `go tool nm <exe>` 符号数 = 0   → 已 strip；
      - 符号数 > 0                      → 未 strip，必须重构建。

    注意 `go version -m` **不记录** `-ldflags`，所以不能靠它判断 strip；
    `go tool nm` 对非 Go 文件也返回 1 且零符号，因此必须先做"是不是 Go 二进制"
    这一步，否则第三方 exe（如 WinSW.exe）会被误判成"已 strip"。

.PARAMETER Path
    要检查的 exe 文件或目录（可多个）。目录会递归查找 *.exe。

.PARAMETER GoExe
    go 可执行文件路径，默认 `go`。

.PARAMETER AllowUnstripped
    只报告不失败（退出码 0）。用于排查历史产物，不要用于发布。

.EXAMPLE
    pwsh deploy/windows/verify-release.ps1 -Path dist

.EXAMPLE
    pwsh deploy/windows/verify-release.ps1 -Path dist/RFERP-server-1.0.0-win64/RFERP-server.exe
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string[]]$Path,
    [string]$GoExe = "go",
    [switch]$AllowUnstripped
)

$ErrorActionPreference = "Stop"

# 先确认本机有 go：发布机一定有（它要编译），但 CI/客户的机器可能没有。
try {
    $null = & $GoExe version 2>&1
    if ($LASTEXITCODE -ne 0) { throw "go version 失败" }
} catch {
    Write-Error "找不到 go（$GoExe）：本脚本依赖 go tool nm 判断 strip。"
    exit 2
}

# 收集目标文件：文件直接用，目录递归找 exe。
$files = @()
foreach ($p in $Path) {
    if (-not (Test-Path $p)) { Write-Error "路径不存在：$p"; exit 2 }
    $item = Get-Item $p
    if ($item.PSIsContainer) {
        $files += @(Get-ChildItem -Path $item.FullName -Filter *.exe -Recurse -File)
    } else {
        $files += @($item)
    }
}
if ($files.Count -eq 0) {
    Write-Error "没有找到任何 .exe：$($Path -join ', ')"
    exit 2
}

$rows = @()
$failed = 0
$thirdParty = 0

foreach ($f in $files) {
    $rel = $f.Name

    # 第 1 步：是不是 Go 二进制。stripped 的也能读出来，这是它能当判据的前提。
    #
    # 原生程序写 stderr 在 $ErrorActionPreference=Stop 下会抛 NativeCommandError，
    # 而"不是 Go 文件"正是本脚本要正常处理的一种情况，所以这两步都必须临时降级。
    $oldEap = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $verOut = & $GoExe version -m $f.FullName 2>$null
    $verExit = $LASTEXITCODE
    $ErrorActionPreference = $oldEap
    if ($verExit -ne 0) {
        $rows += [pscustomobject]@{
            File   = $rel
            Kind   = "第三方"
            Strip  = "未知"
            Detail = "非 Go 二进制，需人工确认来源与哈希"
        }
        $thirdParty++
        continue
    }

    # 第 2 步：符号数。0 = 已 strip。
    # 已 strip 的二进制同样会往 stderr 写 "no symbols"，所以这里也要降级。
    $ErrorActionPreference = "Continue"
    $syms = @(& $GoExe tool nm $f.FullName 2>$null)
    $symCount = $syms.Count
    $ErrorActionPreference = $oldEap

    $verLine = ($verOut | Select-Object -First 1)
    if ($symCount -eq 0) {
        $rows += [pscustomobject]@{
            File   = $rel
            Kind   = "Go"
            Strip  = "已 strip"
            Detail = $verLine
        }
    } else {
        $rows += [pscustomobject]@{
            File   = $rel
            Kind   = "Go"
            Strip  = "未 strip"
            Detail = "$symCount 个符号 —— 必须用 -trimpath -ldflags `"-s -w`" 重构建"
        }
        $failed++
    }
}

$rows | Format-Table -AutoSize

Write-Host ""
Write-Host ("检查 {0} 个文件：未 strip {1}，第三方 {2}" -f $files.Count, $failed, $thirdParty)

if ($thirdParty -gt 0) {
    Write-Host "提示：第三方二进制不在本脚本判据范围内（如 WinSW.exe），需单独锁定来源与 SHA256。" -ForegroundColor Yellow
}

if ($failed -gt 0) {
    if ($AllowUnstripped) {
        Write-Warning "存在未 strip 的二进制（-AllowUnstripped 已放行，请勿用于发布）"
        exit 0
    }
    Write-Host "存在未 strip 的二进制，禁止发布：未 strip 的产物有被杀软判为木马并删除的实测记录。" -ForegroundColor Red
    exit 1
}

Write-Host "全部 Go 二进制已 strip。" -ForegroundColor Green
exit 0
