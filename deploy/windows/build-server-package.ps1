<#
.SYNOPSIS
    构建服务端发布包 RFERP-server-<ver>-win64.zip。

.DESCRIPTION
    产物就是方案 §14 里"主机安装包"那一项，解压后可直接跑 install-host.ps1：
      RFERP-server.exe       已注入版本号（与可选的内置验签公钥）
      WinSW.exe              服务包装器（缺失时自动下载）
      install-host.ps1       主机安装脚本
      uninstall-host.ps1     卸载脚本（保留数据）
      client.config.json     客户端配置示例
      license-public.key     验签公钥（传入 -PublicKeyFile 时）
      README.txt             一页纸安装步骤

    为什么不把 WinSW 提交进仓库：它是外部二进制（18MB），跟着版本走会让仓库
    膨胀且不好升级。构建时下载、打包时带上即可。

.EXAMPLE
    pwsh deploy/windows/build-server-package.ps1 -Version 1.0.0 `
        -PublicKeyFile C:\keys\public.key
#>
[CmdletBinding()]
param(
    [string]$Version = "0.0.0",
    [string]$OutDir = "",
    [string]$PublicKeyFile = "",
    [string]$WinSWUrl = "https://ghfast.top/https://github.com/winsw/winsw/releases/download/v2.12.0/WinSW-x64.exe"
)

$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
if ([string]::IsNullOrWhiteSpace($OutDir)) { $OutDir = Join-Path $root "dist" }
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$OutDir = (Resolve-Path $OutDir).Path

$stage = Join-Path ([System.IO.Path]::GetTempPath()) ("rferp-stage-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $stage | Out-Null

Write-Host "== 编译服务端 ==" -ForegroundColor Cyan
$ldflags = "-s -w -X main.version=$Version"
if ($PublicKeyFile) {
    if (-not (Test-Path $PublicKeyFile)) { throw "找不到公钥文件：$PublicKeyFile" }
    $pub = (Get-Content -Raw $PublicKeyFile).Trim()
    if ($pub.Length -lt 40 -or $pub -match '\s') { throw "公钥文件内容不像 Base64 公钥" }
    $ldflags += " -X app/internal/license.DefaultPublicKeyBase64=$pub"
    Write-Host "  已注入内置验签公钥" -ForegroundColor DarkGray
} else {
    Write-Warning "未传 -PublicKeyFile：安装包不含内置公钥，客户必须把公钥放到 <数据目录>\license-public.key 或设置 RFERP_LICENSE_PUBKEY，否则授权无法验签。"
}
Push-Location $root
try {
    & go build -trimpath -tags production -ldflags $ldflags -o (Join-Path $stage "RFERP-server.exe") ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "go build 失败" }
} finally {
    Pop-Location
}

Write-Host "== 准备 WinSW ==" -ForegroundColor Cyan
$winsw = Join-Path $stage "WinSW.exe"
$cached = Join-Path $root "dist\WinSW.exe"
if (Test-Path $cached) {
    Copy-Item $cached $winsw -Force
    Write-Host "  使用缓存：$cached" -ForegroundColor DarkGray
} else {
    Write-Host "  下载：$WinSWUrl" -ForegroundColor DarkGray
    & curl.exe -sL --max-time 180 -o $winsw $WinSWUrl
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path $winsw)) { throw "WinSW 下载失败（网络受限时可手工放到 dist\WinSW.exe 再重跑）" }
    Copy-Item $winsw $cached -Force
}
$bytes = [System.IO.File]::ReadAllBytes($winsw)
if ($bytes.Length -lt 1MB -or $bytes[0] -ne 0x4D -or $bytes[1] -ne 0x5A) {
    throw "WinSW.exe 不是有效的 Windows 可执行文件（大小 $($bytes.Length) 字节）"
}
Write-Host "  WinSW 校验通过（$([math]::Round($bytes.Length/1MB,1)) MB）" -ForegroundColor DarkGray

Write-Host "== 收集安装脚本与配置 ==" -ForegroundColor Cyan
foreach ($f in @("install-host.ps1", "uninstall-host.ps1", "client.config.json")) {
    Copy-Item (Join-Path $PSScriptRoot $f) $stage -Force
}
if ($PublicKeyFile) {
    Copy-Item $PublicKeyFile (Join-Path $stage "license-public.key") -Force
}
$readme = @"
RFERP 局域网主机安装包 v$Version
================================

1. 把本目录整个复制到主机上（路径不要含空格时最省事）
2. 右键 PowerShell -> 以管理员身份运行，进入本目录执行：
     Set-ExecutionPolicy -Scope Process Bypass -Force
     .\install-host.ps1 -Port 8080
3. 按提示记下：客户端地址（形如 http://192.168.1.10:8080）与初始化令牌
4. 客户端安装后用该令牌创建第一个管理员，再导入 license.rferplic
5. 备份：数据目录下的「备份」；恢复：停服务 -> 替换 rferp.db -> 起服务
   详见交付文档 docs/lan-deployment.md

卸载（保留数据）：.\uninstall-host.ps1 -Port 8080
"@
Set-Content -Path (Join-Path $stage "README.txt") -Value $readme -Encoding UTF8

Write-Host "== 打包 ==" -ForegroundColor Cyan
$zip = Join-Path $OutDir "RFERP-server-$Version-win64.zip"
if (Test-Path $zip) { Remove-Item $zip -Force }
Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zip -CompressionLevel Optimal
Remove-Item $stage -Recurse -Force

$size = [math]::Round((Get-Item $zip).Length / 1MB, 1)
Write-Host ""
Write-Host "完成：$zip（$size MB）" -ForegroundColor Green
