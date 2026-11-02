<#
.SYNOPSIS
    构建客户端安装包（RFERP-<ver>-setup.exe）。

.DESCRIPTION
    只做两件事：编译桌面客户端、调 Inno Setup 打包。
    不做签名——代码签名需要证书，属于交付流程的另一环（见 docs/lan-deployment.md）。

    前置：
      - Go 工具链（与仓库 mise.toml 一致的版本）
      - 前端产物已嵌入：internal/webassets/dist 必须是最新的
        （在 web/ 下执行 npm run build:embed）
      - Inno Setup 6（默认路径 C:\Program Files (x86)\Inno Setup 6\ISCC.exe）

.EXAMPLE
    pwsh deploy/windows/build-client-installer.ps1 -Version 1.0.0
#>
[CmdletBinding()]
param(
    [string]$Version = "0.0.0",
    [string]$OutDir = "",
    [string]$Iscc = "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
)

$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $root "dist"
}
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$OutDir = (Resolve-Path $OutDir).Path

if (-not (Test-Path $Iscc)) {
    throw "找不到 Inno Setup 编译器：$Iscc（装 Inno Setup 6，或用 -Iscc 指定路径）"
}

# 前端产物必须是新的：go:embed 在编译期把 internal/webassets/dist 打进 exe，
# 忘了 build:embed 就会得到一个"界面还是上一版"的安装包。
$assets = Join-Path $root "internal\webassets\dist\index.html"
if (-not (Test-Path $assets)) {
    throw "缺少前端产物（$assets）。请先在 web/ 下执行：npm run build:embed"
}

$dist = Join-Path $OutDir "RFERP.exe"
Write-Host "== 编译客户端 ==" -ForegroundColor Cyan
Push-Location $root
try {
    # 必须带 Wails 的构建标签：不带 desktop,production 的产物启动时会弹
    # "Wails applications will not build without the correct build tags"。
    # -H windowsgui 去掉控制台窗口（界面上不该跟着一个黑框）。
    & go build -trimpath -tags "desktop,production" `
        -ldflags "-s -w -H windowsgui -X main.version=$Version" -o $dist ./cmd/desktop-wails
    if ($LASTEXITCODE -ne 0) { throw "go build 失败" }
} finally {
    Pop-Location
}
Write-Host "  $dist" -ForegroundColor DarkGray

Write-Host "== 打包安装包 ==" -ForegroundColor Cyan
& $Iscc "/DAppVersion=$Version" "/DOutDir=$OutDir" (Join-Path $PSScriptRoot "client-installer.iss")
if ($LASTEXITCODE -ne 0) { throw "ISCC 打包失败（退出码 $LASTEXITCODE）" }

$setup = Join-Path $OutDir "RFERP-$Version-setup.exe"
if (-not (Test-Path $setup)) { throw "没有产出 $setup" }
$size = [math]::Round((Get-Item $setup).Length / 1MB, 1)
Write-Host ""
Write-Host "完成：$setup（$size MB）" -ForegroundColor Green
Write-Host "静默预配置示例：`"$setup`" /VERYSILENT /SERVER=192.168.1.10:8080"
