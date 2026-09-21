@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"
call tools-env.bat || exit /b 1

if "%VERSION%"=="" set VERSION=1.0.0

echo ================================
echo   Build Installer - RFERP v%VERSION%
echo ================================
echo [1/2] Build RFERP.exe ...
go build -ldflags="-H windowsgui -X main.version=%VERSION%" -o RFERP.exe cmd/desktop/main.go
if errorlevel 1 (
    echo   BUILD FAILED!
    pause
    exit /b 1
)
if exist rcedit-x64.exe if exist picture\app.ico (
    rcedit-x64.exe RFERP.exe --set-icon picture\app.ico >nul
    echo       Icon set
)

echo [2/2] Build installer ...
if not defined ISCC (
    echo   [ERROR] ISCC not found. Install Inno Setup 6 or set ISCC.
    pause
    exit /b 1
)
if not exist "!ISCC!" (
    echo   [ERROR] ISCC not found at "!ISCC!".
    pause
    exit /b 1
)
"!ISCC!" /DMyAppVersion=%VERSION% setup.iss
if errorlevel 1 (
    echo   INSTALLER BUILD FAILED!
    pause
    exit /b 1
)

echo.
echo ================================
echo   DONE
echo   Output: %~dp0dist\Setup-RFERP-%VERSION%.exe
echo ================================
pause
