@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"
call tools-env.bat || exit /b 1

if "%VERSION%"=="" set VERSION=1.0.0

echo ================================
echo   Build RFERP v%VERSION%
echo ================================
echo [1/3] go mod tidy ...
go mod tidy
if errorlevel 1 (
    echo   FAILED!
    pause
    exit /b 1
)

echo [2/3] go build ...
go build -ldflags="-linkmode=internal -H windowsgui -X main.version=%VERSION%" -o app.exe cmd/desktop/main.go
if errorlevel 1 (
    echo   FAILED!
    pause
    exit /b 1
)

echo [3/3] set icon ...
if exist rcedit-x64.exe if exist picture\app.ico (
    rcedit-x64.exe app.exe --set-icon picture\app.ico >nul
    echo       Icon set
)
move /y app.exe RFERP.exe >nul 2>nul

echo.
echo ================================
echo   DONE
echo   Output: %~dp0RFERP.exe
echo ================================
pause
