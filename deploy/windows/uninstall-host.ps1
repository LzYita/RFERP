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

$svcExe = Join-Path $InstallDir "RFERP-server-service.exe"
if (Test-Path $svcExe) {
    Push-Location $InstallDir
    try {
        & $svcExe stop 2>$null | Out-Null
        & $svcExe uninstall 2>$null | Out-Null
    } finally {
        Pop-Location
    }
    Write-Host "服务已卸载。"
} else {
    Write-Host "未找到服务包装器，跳过服务卸载。"
}

$ruleName = "RFERP 局域网 $Port"
if (Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue) {
    Remove-NetFirewallRule -DisplayName $ruleName
    Write-Host "防火墙规则已移除：$ruleName"
}

Write-Host ""
Write-Host "数据目录未被删除。确认不再需要后，可手动删除 server.json 所在目录。" -ForegroundColor Yellow
