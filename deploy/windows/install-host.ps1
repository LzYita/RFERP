<#
.SYNOPSIS
    安装 RFERP 局域网主机：服务、配置、防火墙规则、启动与自检。

.DESCRIPTION
    设计目标（方案 §8.3 / FR-41）：非开发人员按文档就能跑完。
    因此这里只做五件事，每件都可重复执行（幂等）：
      1. 校验权限 / 端口 / 数据目录
      2. 生成 server.json（storage=sqlite，与服务端 mode 无关）
      3. 把 RFERP-server.exe 直接注册为 Windows 服务（SCM 原生，无第三方包装器）
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
    安装包解压目录（含 RFERP-server.exe）
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
if (-not (Test-Path $serverExe)) { Fail "找不到 $serverExe（请把安装包解压后从该目录运行本脚本）" }
Copy-Item $serverExe $InstallDir -Force
$serverExe = Join-Path $InstallDir "RFERP-server.exe"
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

# ---------- 7. 注册服务（SCM 原生） ----------
#
# 以前这里用 WinSW 把 exe 包成服务。换成原生之后：
#   - 交付包里不再有第三方未签名 exe（WinSW 约 18MB，从加速器下载、只校验 MZ 头）；
#   - 失败重启用 SCM 自己的 failure actions 表达，语义更直白；
#   - 服务进程不再有"标准输出被包装器收走"的地方，所以给服务注册事件日志源：
#     启动失败的原因会出现在「事件查看器 → Windows 日志 → 应用程序」（internal/svcrun）。
$svcName = "RFERPServer"
$existing = Get-CimInstance Win32_Service -Filter "Name='$svcName'" -ErrorAction SilentlyContinue
if ($existing) {
    Info "服务已存在，先停止并卸载旧注册"
    & sc.exe stop $svcName | Out-Null
    Start-Sleep -Seconds 2
    & sc.exe delete $svcName | Out-Null
    Start-Sleep -Seconds 2
}

# 事件日志源：svcrun 用它写"启动失败：…"。注册失败不致命——会降级为只写文件日志。
try {
    if (-not [System.Diagnostics.EventLog]::SourceExists($svcName)) {
        New-EventLog -LogName Application -Source $svcName -ErrorAction Stop
        Info "事件日志源：$svcName ✓"
    } else {
        Info "事件日志源：$svcName 已存在 ✓"
    }
} catch {
    Info "事件日志源：未注册（不影响安装；启动失败原因看数据目录的日志）"
}

# PathName 必须把 exe 路径整体加引号：安装目录含空格（Program Files）。
# 用 WMI 的 Create 而不是 sc.exe create，是为了绕开 PowerShell 5.1 对
# 内嵌引号的原生参数转义问题——PathName 在这里是一个普通字符串参数。
$binPath = '"{0}" -config "{1}"' -f $serverExe, $configPath
$create = Invoke-CimMethod -ClassName Win32_Service -MethodName Create -Arguments @{
    Name         = $svcName
    DisplayName  = "RFERP 局域网服务"
    Description  = "RFERP 仓库管理系统的局域网主机服务（SQLite）。"
    PathName     = $binPath
    ServiceType  = [uint32]16   # 0x10 = 独立进程
    ErrorControl = [uint32]1    # 1 = 正常（记录错误，不阻止启动）
    StartMode    = "Automatic"
    StartName    = "NT AUTHORITY\LocalService"
}
if ($create.ReturnValue -ne 0) {
    Fail "注册服务失败（Win32_Service.Create 返回 $($create.ReturnValue)）"
}
Info "服务：$svcName 已注册（LocalService / 自动启动）✓"

# 失败重启：10 秒后重启，最多 3 次；1 小时后重置计数。
# 等价于原 WinSW 的 <onfailure action="restart" delay="10 sec"/> + <resetfailure>。
& sc.exe failure $svcName reset= 3600 actions= restart/10000/restart/10000/restart/10000 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "  警告：设置失败重启策略失败（sc.exe 返回 $LASTEXITCODE），服务仍可用" -ForegroundColor Yellow
} else {
    Info "失败重启：10 秒后重启 ×3，1 小时重置计数 ✓"
}

try {
    Start-Service -Name $svcName -ErrorAction Stop
    Info "服务已启动 ✓"
} catch {
    # Start-Service 的报错只说"服务没有及时响应"，真正的原因（配置错、
    # 库打不开、迁移失败）在事件日志与数据目录日志里。
    Fail ("服务启动失败：{0}`n原因见「事件查看器 → Windows 日志 → 应用程序」里来源 {1} 的记录，" +
          "以及数据目录下的「日志」文件夹。" -f $_.Exception.Message, $svcName)
}

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
    Fail "服务未在 60 秒内就绪。请查看数据目录下的「日志」文件夹，以及「事件查看器 → Windows 日志 → 应用程序」里来源为 $svcName 的记录。"
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
