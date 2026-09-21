@echo off
rem Locate build tools. Relies on system PATH; honors these env overrides:
rem   GOROOT   - Go root (its bin is added to PATH if present)
rem   GCC_DIR  - GCC root (its bin is added to PATH if present)
rem   GH       - full path to gh.exe (default: gh from PATH)
rem   ISCC     - full path to ISCC.exe (default: Inno Setup 6 install dir)
rem Usage from another script:  call tools-env.bat || exit /b 1

if defined GOROOT if exist "%GOROOT%\bin" set "PATH=%GOROOT%\bin;%PATH%"
if defined GCC_DIR if exist "%GCC_DIR%\bin" set "PATH=%GCC_DIR%\bin;%PATH%"

where go >nul 2>nul || ( echo [ERROR] "go" not found in PATH. Install Go or set GOROOT. & exit /b 1 )
set "CGO_ENABLED=1"
set "CC=gcc"
where gcc >nul 2>nul || ( echo [ERROR] "gcc" not found in PATH. Install a C compiler or set GCC_DIR. & exit /b 1 )

if not defined GH set "GH=gh"
where gh >nul 2>nul
if errorlevel 1 if "%GH%"=="gh" set "GH="

if not defined ISCC set "ISCC=%ProgramFiles(x86)%\Inno Setup 6\ISCC.exe"
if not exist "%ISCC%" set "ISCC=%ProgramFiles%\Inno Setup 6\ISCC.exe"

exit /b 0
