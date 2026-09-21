@echo off
setlocal enabledelayedexpansion
set "REPO=LzYita/RFERP"
set "GH=D:\GitHub CLI\gh.exe"
set "KEY=%USERPROFILE%\.rferp\update-signing.key"
set "ISCC=C:\Program Files (x86)\Inno Setup 6\ISCC.exe"

set "VERSION=%~1"
if "%VERSION%"=="" set /p "VERSION=Version (e.g. 1.2.3): "
if "%VERSION%"=="" (
    echo No version given.
    pause
    exit /b 1
)

set "TAG=v%VERSION%"
set "ASSET_BASE=https://github.com/%REPO%/releases/download/%TAG%"

set "PATH=D:\Go\bin;D:\TDM-GCC\bin;%PATH%"
set "CGO_ENABLED=1"
set "CC=D:\TDM-GCC\bin\gcc.exe"
cd /d D:\opencode\sql_project

echo ================================
echo   Release RFERP %VERSION%
echo ================================

echo [1/6] Check signing key ...
if not exist "%KEY%" (
    echo   Missing private key: %KEY%
    pause
    exit /b 1
)

echo [2/6] Build RFERP.exe ...
go build -ldflags="-linkmode=internal -H windowsgui -X main.version=%VERSION%" -o RFERP.exe cmd/desktop/main.go
if errorlevel 1 (
    echo   Build failed.
    pause
    exit /b 1
)
if exist rcedit-x64.exe if exist picture\app.ico rcedit-x64.exe RFERP.exe --set-icon picture\app.ico >nul

echo [3/6] Package update zip ...
if not exist dist mkdir dist
if exist "dist\RFERP-%VERSION%.zip" del "dist\RFERP-%VERSION%.zip"
powershell -NoProfile -Command "Compress-Archive -Path 'RFERP.exe' -DestinationPath 'dist\RFERP-%VERSION%.zip' -Force"
if errorlevel 1 (
    echo   Package failed.
    pause
    exit /b 1
)

echo [4/6] Sign manifest ...
go run ./cmd/signmanifest -key "%KEY%" -zip "dist\RFERP-%VERSION%.zip" -url "%ASSET_BASE%/RFERP-%VERSION%.zip" -version "%VERSION%" -notes "RFERP %VERSION%" -out "dist\releases.json"
if errorlevel 1 (
    echo   Signing failed.
    pause
    exit /b 1
)

echo [5/6] Build installer ...
if not exist "%ISCC%" (
    echo   ISCC not found: %ISCC%
    pause
    exit /b 1
)
"%ISCC%" /DMyAppVersion=%VERSION% setup.iss >nul
if errorlevel 1 (
    echo   Installer build failed.
    pause
    exit /b 1
)

echo [6/6] Upload to GitHub Release ...
"%GH%" auth status >nul 2>nul
if errorlevel 1 (
    echo   gh not logged in. Run: gh auth login
    pause
    exit /b 1
)
"%GH%" release create %TAG% --draft --target main --title "RFERP %VERSION%" --notes "RFERP %VERSION%" "dist\Setup-RFERP-%VERSION%.exe" "dist\RFERP-%VERSION%.zip" "dist\releases.json"
if errorlevel 1 (
    echo   Release create failed.
    pause
    exit /b 1
)
"%GH%" release edit %TAG% --draft=false --latest
if errorlevel 1 (
    echo   Release publish failed.
    pause
    exit /b 1
)

echo.
echo ================================
echo   DONE
echo   Manifest URL: https://github.com/%REPO%/releases/latest/download/releases.json
echo   Installer:    %ASSET_BASE%/Setup-RFERP-%VERSION%.exe
echo ================================
pause
