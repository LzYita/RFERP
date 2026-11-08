<#
.SYNOPSIS
    构建服务端发布包 RFERP-server-<ver>-win64.zip。

.DESCRIPTION
    产物就是方案 §14 里"主机安装包"那一项，解压后可直接跑 install-host.ps1：
      RFERP-server.exe       已注入版本号（与可选的内置验签公钥）；本身即 Windows 服务
      install-host.ps1       主机安装脚本
      uninstall-host.ps1     卸载脚本（保留数据）
      client.config.json     客户端配置示例
      license-public.key     验签公钥（传入 -PublicKeyFile 时）
      README.txt             一页纸安装步骤

    包内**没有**第三方二进制：服务由 SCM 原生启动（见 internal/svcrun），
    不再需要 WinSW 这类包装器。1.0.0 曾打进 WinSW-x64.exe（约 18MB，从 GitHub
    加速器下载、只校验 MZ 头），既增加供应链面，也是交付包里唯一未签名的
    第三方 exe。

.EXAMPLE
    pwsh deploy/windows/build-server-package.ps1 -Version 1.0.0 `
        -PublicKeyFile C:\keys\public.key
#>
[CmdletBinding()]
param(
    [string]$Version = "0.0.0",
    [string]$OutDir = "",
    [string]$PublicKeyFile = ""
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

# strip 闸门（P3）：未 strip 的产物有被杀软判为木马并删除的实测记录
# （见 docs/plans/av-false-positive-remediation.md）。这里用脚本拦，
# 不靠构建者记得 -s -w——本脚本本身已经带了，拦的是"以后有人改坏"。
& (Join-Path $PSScriptRoot "verify-release.ps1") -Path (Join-Path $stage "RFERP-server.exe")
if ($LASTEXITCODE -ne 0) { throw "发布前自检未通过：RFERP-server.exe 未 strip 或不是有效 Go 二进制" }

Write-Host "== 收集安装脚本与配置 ==" -ForegroundColor Cyan
foreach ($f in @("install-host.ps1", "uninstall-host.ps1", "client.config.json", "first-run-blocked.md")) {
    Copy-Item (Join-Path $PSScriptRoot $f) $stage -Force
}
if ($PublicKeyFile) {
    Copy-Item $PublicKeyFile (Join-Path $stage "license-public.key") -Force
}
$readme = @"
RFERP 局域网主机安装包 v$Version
================================

1. 把本目录整个复制到主机上（路径不要含空格时最省事）
2. 先做一件事：把本目录和数据目录加入杀毒软件信任区（详见 first-run-blocked.md）
3. 右键 PowerShell -> 以管理员身份运行，进入本目录执行：
     Set-ExecutionPolicy -Scope Process Bypass -Force
     .\install-host.ps1 -Port 8080
4. 按提示记下：客户端地址（形如 http://192.168.1.10:8080）与初始化令牌
5. 客户端安装后用该令牌创建第一个管理员，再导入 license.rferplic
6. 备份：数据目录下的「备份」；恢复：停服务 -> 替换 rferp.db -> 起服务
   详见交付文档 docs/lan-deployment.md

卸载（保留数据）：.\uninstall-host.ps1 -Port 8080

文件完整性：SHA256SUMS.txt 列出包内每个文件的哈希；
被杀软拦截或怀疑文件被动过时，用 certutil -hashfile <文件> SHA256 核对。
"@
Set-Content -Path (Join-Path $stage "README.txt") -Value $readme -Encoding UTF8

# SHA256SUMS.txt：客户侧核对文件完整性（防杀软误删后拿错文件、防传输损坏）。
# 不含它自己；格式与 sha256sum -c 兼容（Git Bash），Windows 下用
# certutil -hashfile <文件> SHA256 逐个核对（见 first-run-blocked.md）。
#
# 必须用 .NET WriteAllText 写 LF：Set-Content 在 PS 5.1 下写 CRLF，
# sha256sum -c 会把行尾的 \r 当成文件名的一部分，全部报 "No such file"。
Write-Host "== 生成 SHA256SUMS.txt ==" -ForegroundColor Cyan
$sums = Get-ChildItem -Path $stage -File | Sort-Object Name | ForEach-Object {
    "{0}  {1}" -f (Get-FileHash -Algorithm SHA256 -Path $_.FullName).Hash.ToLowerInvariant(), $_.Name
}
[IO.File]::WriteAllText((Join-Path $stage "SHA256SUMS.txt"), ($sums -join "`n") + "`n")

Write-Host "== 打包 ==" -ForegroundColor Cyan
$zip = Join-Path $OutDir "RFERP-server-$Version-win64.zip"
if (Test-Path $zip) { Remove-Item $zip -Force }
Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zip -CompressionLevel Optimal
Remove-Item $stage -Recurse -Force

$size = [math]::Round((Get-Item $zip).Length / 1MB, 1)
$zipHash = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
Write-Host ""
Write-Host "完成：$zip（$size MB）" -ForegroundColor Green
Write-Host "zip SHA256：$zipHash" -ForegroundColor DarkGray
