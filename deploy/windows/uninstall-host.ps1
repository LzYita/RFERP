<#
.SYNOPSIS
    卸载 RFERP 局域网主机服务（保留数据目录）。

.DESCRIPTION
    只移除服务与防火墙规则，**不删除数据**：
    数据目录里有客户的全部业务数据、备份、授权与设备记录。
    要彻底清理，请人工确认后再删目录。
#>
[CmdletBinding()]
param(
    [string]$InstallDir = "C:\Program Files\RFERP",
    [int]$Port = 8080
)

$ErrorActionPreference = "Stop"

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "需要管理员权限。" -ForegroundColor Red
    exit 1
}

$svcName = "RFERPServer"
$svc = Get-CimInstance Win32_Service -Filter "Name='$svcName'" -ErrorAction SilentlyContinue
if ($svc) {
    & sc.exe stop $svcName | Out-Null
    Start-Sleep -Seconds 2
    & sc.exe delete $svcName | Out-Null
    if ($LASTEXITCODE -eq 0) {
        Write-Host "服务已卸载：$svcName"
    } else {
        Write-Host "服务删除失败（sc.exe 返回 $LASTEXITCODE），请到 services.msc 手工删除。" -ForegroundColor Yellow
    }
} else {
    Write-Host "未找到服务 $svcName，跳过服务卸载。"
}

# 兼容 1.0.0 用 WinSW 安装的旧部署：如果还留着包装器，一并清掉，
# 否则升级到原生服务后旧包装器会继续占着服务名，新注册会失败。
$legacyWrapper = Join-Path $InstallDir "RFERP-server-service.exe"
if (Test-Path $legacyWrapper) {
    $legacy = Get-CimInstance Win32_Service -Filter "Name='$svcName'" -ErrorAction SilentlyContinue
    if ($legacy) {
        & sc.exe stop $svcName | Out-Null
        Start-Sleep -Seconds 2
        & sc.exe delete $svcName | Out-Null
    }
    Remove-Item $legacyWrapper -Force -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $InstallDir "RFERP-server-service.xml") -Force -ErrorAction SilentlyContinue
    Write-Host "已移除旧版 WinSW 包装器。"
}

$ruleName = "RFERP 局域网 $Port"
if (Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue) {
    Remove-NetFirewallRule -DisplayName $ruleName
    Write-Host "防火墙规则已移除：$ruleName"
}

Write-Host ""
Write-Host "数据目录未被删除。确认不再需要后，可手动删除 server.json 所在目录。" -ForegroundColor Yellow
