@echo off
setlocal
if "%VERSION%"=="" set VERSION=1.0.0
set PATH=D:\Go\bin;D:\TDM-GCC\bin;%PATH%
set CGO_ENABLED=1
set CC=D:\TDM-GCC\bin\gcc.exe
cd /d D:\opencode\sql_project

echo ================================
echo   Build Installer - RFERP v%VERSION%
echo ================================
echo [1/2] Building RFERP.exe ...
go build -ldflags="-linkmode=internal -H windowsgui -X main.version=%VERSION%" -o RFERP.exe cmd/desktop/main.go
if errorlevel 1 (
    echo BUILD FAILED!
    pause
    exit /b 1
)
if exist rcedit-x64.exe if exist picture\app.ico (
    rcedit-x64.exe RFERP.exe --set-icon picture\app.ico >nul
    echo       Icon set
)

echo [2/2] Building installer ...
"C:\Program Files (x86)\Inno Setup 6\ISCC.exe" /DMyAppVersion=%VERSION% setup.iss
if errorlevel 1 (
    echo INSTALLER BUILD FAILED!
    pause
    exit /b 1
)

echo.
echo ================================
echo   DONE: dist\Setup-RFERP-%VERSION%.exe
echo ================================
pause
