<#
.SYNOPSIS
    安装 RFERP 局域网主机：服务、配置、防火墙规则、启动与自检。

.DESCRIPTION
    设计目标（方案 §8.3 / FR-41）：非开发人员按文档就能跑完。
    因此这里只做五件事，每件都可重复执行（幂等）：
      1. 校验权限 / 端口 / 数据目录
      2. 生成 server.json（storage=sqlite，与服务端 mode 无关）
      3. 用 WinSW 注册 Windows 服务
      4. 加一条只放行 LocalSubnet 的 TCP 防火墙规则
      5. 启动并等 /readyz，打印客户端要填的地址与初始化令牌位置

    不做的事：不装 WebView2、不动杀毒软件、不建数据库——这些要么是
    客户端/系统层的事，要么由服务端首次启动自动完成。

.PARAMETER InstallDir
    程序目录，默认 C:\Program Files\RFERP
.PARAMETER DataDir
    数据目录，默认 D:\RFERP数据（不存在则用 C:\ProgramData\RFERP）
.PARAMETER Port
    监听端口，默认 8080
.PARAMETER SourceDir
    安装包解压目录（含 RFERP-server.exe / WinSW.exe）
.PARAMETER LicensePublicKeyFile
    授权验签公钥文件（发布构建已内置时可省略）
#>
[CmdletBinding()]
param(
    [string]$InstallDir = "C:\Program Files\RFERP",
    [string]$DataDir = "",
    [int]$Port = 8080,
    [string]$SourceDir = $PSScriptRoot,
    [string]$LicensePublicKeyFile = ""
)

$ErrorActionPreference = "Stop"

function Fail($msg) {
    Write-Host ""
    Write-Host "安装失败：$msg" -ForegroundColor Red
    exit 1
}

function Info($msg) { Write-Host "  $msg" }

Write-Host "== RFERP 局域网主机安装 ==" -ForegroundColor Cyan

# ---------- 1. 权限 ----------
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail "需要管理员权限。请右键 PowerShell -> 以管理员身份运行，再执行本脚本。"
}
Info "权限：管理员 ✓"

# ---------- 2. 系统检查 ----------
$os = Get-CimInstance Win32_OperatingSystem
if ($os.Caption -notmatch "Windows 1[01]") {
    Write-Host "  警告：当前系统是 $($os.Caption)，建议 Windows 10/11 64 位。" -ForegroundColor Yellow
}
$cores = (Get-CimInstance Win32_Processor | Measure-Object -Property NumberOfCores -Sum).Sum
$memGB = [math]::Round($os.TotalVisibleMemorySize / 1MB, 1)
Info "硬件：$cores 核 / $memGB GB"
if ($cores -lt 4 -or $memGB -lt 7) {
    Write-Host "  警告：低于建议配置（4 核 / 8GB），多客户端并发时可能变慢。" -ForegroundColor Yellow
}

# ---------- 3. 端口检查 ----------
$inUse = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
if ($inUse) {
    Fail "端口 $Port 已被占用（$($inUse.OwningProcess | Select-Object -First 1)）。请改用 -Port 18080 重试。"
}
Info "端口：$Port 空闲 ✓"

# ---------- 4. 数据目录 ----------
if ([string]::IsNullOrWhiteSpace($DataDir)) {
    if (Test-Path "D:\") { $DataDir = "D:\RFERP数据" } else { $DataDir = "C:\ProgramData\RFERP" }
}
if ($DataDir -match "^\\\\" -or $DataDir -match "^[A-Za-z]:\\?$") {
    Fail "数据目录不能是网络共享或盘符根目录：$DataDir"
}
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir "备份") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir "日志") | Out-Null
Info "数据目录：$DataDir ✓"

# ACL：Administrators/SYSTEM 完全控制；LocalService 修改权限。
# 服务以 LocalService 运行时若没有这一条，SQLite 文件与备份目录都写不进去（R16）。
$acl = Get-Acl $DataDir
foreach ($id in @("BUILTIN\Administrators", "NT AUTHORITY\SYSTEM")) {
    $acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($id, "FullControl", "ContainerInherit,ObjectInherit", "None", "Allow")))
}
$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule("NT AUTHORITY\LOCAL SERVICE", "Modify", "ContainerInherit,ObjectInherit", "None", "Allow")))
Set-Acl -Path $DataDir -AclObject $acl
Info "目录权限：LocalService 可写 ✓"

# ---------- 5. 复制程序 ----------
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$serverExe = Join-Path $SourceDir "RFERP-server.exe"
$winswExe = Join-Path $SourceDir "WinSW.exe"
if (-not (Test-Path $serverExe)) { Fail "找不到 $serverExe（请把安装包解压后从该目录运行本脚本）" }
if (-not (Test-Path $winswExe)) { Fail "找不到 $winswExe" }
Copy-Item $serverExe $InstallDir -Force
$svcExe = Join-Path $InstallDir "RFERP-server-service.exe"
Copy-Item $winswExe $svcExe -Force
Info "程序目录：$InstallDir ✓"

# 授权公钥：安装包内置过就不用带；否则落到数据目录，服务端启动时自己读。
if ($LicensePublicKeyFile) {
    if (-not (Test-Path $LicensePublicKeyFile)) { Fail "找不到公钥文件 $LicensePublicKeyFile" }
    Copy-Item $LicensePublicKeyFile (Join-Path $DataDir "license-public.key") -Force
    Info "授权公钥：已复制到数据目录 ✓"
}

# ---------- 6. 写 server.json ----------
# 关键：服务端配置与客户端配置分开文件（R10）。这里生成的 server.json 只描述
# 存储与监听；不带 mode，避免服务端误读客户端配置。
$configPath = Join-Path $DataDir "server.json"
$config = [ordered]@{
    dataDir = ($DataDir -replace "\\", "/")
    storage = "sqlite"
    server  = @{ addr = ":$Port" }
}
$config | ConvertTo-Json -Depth 4 | Set-Content -Path $configPath -Encoding UTF8
Info "配置：$configPath ✓"

# ---------- 7. 注册服务（WinSW v2） ----------
$svcName = "RFERPServer"
$existing = Get-Service -Name $svcName -ErrorAction SilentlyContinue
if ($existing) {
    Info "服务已存在，先停止并卸载旧注册"
    & $svcExe stop 2>$null | Out-Null
    & $svcExe uninstall 2>$null | Out-Null
    Start-Sleep -Seconds 2
}

$winswXml = @"
<service>
  <id>$svcName</id>
  <name>RFERP 局域网服务</name>
  <description>RFERP 仓库管理系统的局域网主机服务（SQLite）。</description>
  <executable>%BASE%\RFERP-server.exe</executable>
  <arguments>-config "$configPath"</arguments>
  <workingdirectory>%BASE%</workingdirectory>
  <env name="RFERP_DATA_DIR" value="$DataDir"/>
  <logpath>%BASE%\logs</logpath>
  <log mode="roll-by-size">
    <sizeThreshold>10240</sizeThreshold>
    <keepFiles>10</keepFiles>
  </log>
  <onfailure action="restart" delay="10 sec"/>
  <resetfailure>1 hour</resetfailure>
  <startmode>Automatic</startmode>
  <stoptimeout>30 sec</stoptimeout>
  <serviceaccount>
    <username>NT AUTHORITY\LocalService</username>
    <allowservicelogon>true</allowservicelogon>
  </serviceaccount>
</service>
"@
Set-Content -Path (Join-Path $InstallDir "RFERP-server-service.xml") -Value $winswXml -Encoding UTF8

Push-Location $InstallDir
try {
    & $svcExe install | Out-Null
    & $svcExe start | Out-Null
} finally {
    Pop-Location
}
Info "服务：$svcName 已注册并启动 ✓"

# ---------- 8. 防火墙（只放行局域网） ----------
$ruleName = "RFERP 局域网 $Port"
Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Protocol TCP -LocalPort $Port `
    -RemoteAddress LocalSubnet -Action Allow -Profile Any | Out-Null
Info "防火墙：仅允许 LocalSubnet 访问 TCP $Port ✓（公网/4G 不可达）"

# ---------- 9. 自检 ----------
$ready = $false
for ($i = 0; $i -lt 30; $i++) {
    try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/readyz" -UseBasicParsing -TimeoutSec 3
        if ($r.StatusCode -eq 200) { $ready = $true; break }
    } catch { Start-Sleep -Seconds 2 }
}
if (-not $ready) {
    Write-Host ""
    Fail "服务未在 60 秒内就绪。请查看 $InstallDir\logs 与 $DataDir\日志 下的日志。"
}
Info "就绪检查：/readyz 200 ✓"

# ---------- 10. 交付信息 ----------
Write-Host ""
Write-Host "== 安装完成 ==" -ForegroundColor Green
Write-Host "客户端可填写的地址（同一局域网内）："
Get-NetIPAddress -AddressFamily IPv4 |
    Where-Object { $_.IPAddress -notlike "127.*" -and $_.IPAddress -notlike "169.254.*" } |
    ForEach-Object { Write-Host "    http://$($_.IPAddress):$Port" -ForegroundColor Cyan }

$tokenFile = Join-Path $DataDir "初始化令牌.txt"
if (Test-Path $tokenFile) {
    Write-Host ""
    Write-Host "首次初始化令牌（第一台客户端创建管理员时输入）：" -ForegroundColor Yellow
    Get-Content $tokenFile | Where-Object { $_ -match "^[A-Z0-9]{8}$" } | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
    Write-Host "令牌文件：$tokenFile（创建管理员后服务器不再校验，可删除）"
}
Write-Host ""
Write-Host "数据目录：$DataDir"
Write-Host "服务名  ：$svcName（services.msc 可查看；开机自启、失败自动重启）"
Write-Host "自检命令：& '$InstallDir\RFERP-server.exe' -healthcheck -addr :$Port"
Write-Host ""
