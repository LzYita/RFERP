@echo off
echo ================================
echo   Build Tool - RFERP
echo ================================
echo.
echo [1/3] Building...
set PATH=D:\Go\bin;D:\TDM-GCC\bin;%PATH%
set CGO_ENABLED=1
set CC=D:\TDM-GCC\bin\gcc.exe
cd /d D:\opencode\sql_project
echo       Go:     ok
echo       GCC:    ok
echo.
echo [2/3] go mod tidy...
go mod tidy
echo.
echo [3/3] go build...
rem linkmode=internal avoids gcc external linker PE issues
rem -H windowsgui hides the console window
if "%VERSION%"=="" set VERSION=1.0.0
go build -ldflags="-linkmode=internal -H windowsgui -X main.version=%VERSION%" -o app.exe cmd/desktop/main.go
if errorlevel 1 (
    echo FAILED!
    pause
    exit /b
)
echo       Build OK
if exist rcedit-x64.exe if exist picture\app.ico (
    rcedit-x64.exe app.exe --set-icon picture\app.ico >nul
    echo       Icon set
)
move /y app.exe RFERP.exe >nul 2>nul
echo.
echo ================================
echo   DONE!
echo   Output: RFERP.exe
echo ================================
pause
