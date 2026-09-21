# 新电脑一键安装脚本
# 用法：右键 → 使用 PowerShell 运行

$ProjectDir = "D:\opencode\sql_project"
$GoRoot = "D:\Go"
$GccDir = "D:\TDM-GCC"
$MysqlDir = "D:\MySQL\MySQL Server 8.0"

Write-Host "===== 安装检测 =====" -ForegroundColor Cyan

# ---- 检查路径 ----
$ok = $true
$checks = @(
    @{Path="$GoRoot\bin\go.exe"; Name="Go编译器"},
    @{Path="$GccDir\bin\gcc.exe"; Name="GCC"},
    @{Path="$MysqlDir\bin\mysqld.exe"; Name="MySQL"},
    @{Path="$ProjectDir\migrations\001_init.sql"; Name="数据库脚本"}
)
foreach ($c in $checks) {
    if (Test-Path $c.Path) {
        Write-Host "  [OK] $($c.Name)" -ForegroundColor Green
    } else {
        Write-Host "  [ERR] 未找到 $($c.Name): $($c.Path)" -ForegroundColor Red
        $ok = $false
    }
}
if (-not $ok) { Read-Host "按回车退出"; exit }

# ---- 1. 环境变量 ----
Write-Host "`n[1/5] 配置环境变量..." -ForegroundColor Yellow
try {
    $p = [Environment]::GetEnvironmentVariable("Path", "User")
    $add = @("$GoRoot\bin", "$GccDir\bin", "$MysqlDir\bin")
    foreach ($a in $add) {
        if ($p -notlike "*$a*") { $p = "$a;$p" }
    }
    [Environment]::SetEnvironmentVariable("Path", $p, "User")
    [Environment]::SetEnvironmentVariable("GOROOT", $GoRoot, "User")
    Write-Host "  [OK] PATH 已添加 Go/GCC/MySQL" -ForegroundColor Green
} catch { Write-Host "  [WARN] 手动设置: 将以下路径加入系统环境变量" -ForegroundColor Yellow }

# 当前进程使用
$env:GOROOT = $GoRoot
$env:Path = "$GoRoot\bin;$GccDir\bin;$MysqlDir\bin;$env:Path"
$env:CGO_ENABLED = "1"
$env:CC = "$GccDir\bin\gcc.exe"

# ---- 2. 修复 DLL ----
Write-Host "`n[2/5] 修复 GCC DLL..." -ForegroundColor Yellow
Copy-Item "$GccDir\bin\libgcc_s_seh_64-1.dll" "$GccDir\bin\libgcc_s_seh-1.dll" -Force
Copy-Item "$GccDir\bin\libstdc++_64-6.dll" "$GccDir\bin\libstdc++-6.dll" -Force
Copy-Item "$GccDir\bin\libwinpthread_64-1.dll" "$GccDir\bin\libwinpthread-1.dll" -Force
Write-Host "  [OK]" -ForegroundColor Green

# ---- 2. 启动 MySQL ----
Write-Host "`n[3/5] 启动 MySQL..." -ForegroundColor Yellow
$svc = Get-Service -Name MySQL80 -ErrorAction SilentlyContinue
if (-not $svc) {
    Write-Host "  安装 MySQL 服务..."
    & "$MysqlDir\bin\mysqld" --install MySQL80
    Start-Sleep 2
    $svc = Get-Service -Name MySQL80
}
if ($svc.Status -ne "Running") {
    Start-Service MySQL80
    Start-Sleep 3
}
Write-Host "  [OK] MySQL 已启动" -ForegroundColor Green

# ---- 3. 建库 + 导表 ----
Write-Host "`n[4/5] 初始化数据库..." -ForegroundColor Yellow
$rootPwd = Read-Host "请输入 MySQL root 密码" -AsSecureString
$plain = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
    [Runtime.InteropServices.Marshal]::SecureStringToBSTR($rootPwd))

Write-Host "  创建数据库..."
& "$MysqlDir\bin\mysql" -u root -p$plain -e "CREATE DATABASE IF NOT EXISTS appliancedb DEFAULT CHARSET utf8mb4" 2>$null

Write-Host "  导入表结构..."
$sql = Get-Content "$ProjectDir\migrations\001_init.sql" -Raw
& "$MysqlDir\bin\mysql" -u root -p$plain appliancedb -e "$sql" 2>$null

Write-Host "  创建专用最小权限账号..."
$chars = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
$dbUser = 'mes_app'
$dbPass = -join (1..24 | ForEach-Object { $chars[(Get-Random -Maximum $chars.Length)] })
$grant = "CREATE USER IF NOT EXISTS '$dbUser'@'localhost' IDENTIFIED BY '$dbPass'; " +
         "ALTER USER '$dbUser'@'localhost' IDENTIFIED BY '$dbPass'; " +
         "GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,INDEX,DROP,REFERENCES ON appliancedb.* TO '$dbUser'@'localhost'; " +
         "FLUSH PRIVILEGES;"
& "$MysqlDir\bin\mysql" -u root -p$plain -e $grant 2>$null
if ($LASTEXITCODE -ne 0) {
    Write-Host "  [WARN] 专用账号创建失败，回退使用 root" -ForegroundColor Yellow
    $dbUser = 'root'
    $dbPass = $plain
}

# ---- 4. 生成配置文件（DPAPI 加密口令） ----
Write-Host "`n[5/5] 生成配置文件 + 编译..." -ForegroundColor Yellow
Add-Type -AssemblyName System.Security
$enc = [System.Security.Cryptography.ProtectedData]::Protect(
    [System.Text.Encoding]::UTF8.GetBytes($dbPass), $null,
    [System.Security.Cryptography.DataProtectionScope]::CurrentUser)
$cfg = [ordered]@{
    host          = '127.0.0.1'
    port          = 3306
    user          = $dbUser
    password_enc  = [Convert]::ToBase64String($enc)
    dbname        = 'appliancedb'
    dataDir       = 'D:\仓库数据'
    mysqlService  = 'MySQL80'
    mysqldumpPath = "$MysqlDir\bin\mysqldump.exe"
}
$json = $cfg | ConvertTo-Json
[System.IO.File]::WriteAllText("$ProjectDir\config.json", $json, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "  [OK] config.json 已生成（口令已加密）" -ForegroundColor Green

Set-Location $ProjectDir

Write-Host "  下载依赖..."
go env -w GOPROXY=https://goproxy.cn,direct
go mod tidy

Write-Host "  编译..."
go build -o "RFERP.exe" cmd/desktop/main.go

# 设置图标
if ((Test-Path "rcedit-x64.exe") -and (Test-Path "picture\app.ico")) {
    .\rcedit-x64.exe "RFERP.exe" --set-icon picture\app.ico >$null 2>$null
}

Write-Host "`n===== 安装完成 ======" -ForegroundColor Cyan
Write-Host "双击 RFERP.exe 启动" -ForegroundColor White
Read-Host "按回车退出"
