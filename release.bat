@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"
call tools-env.bat || exit /b 1

rem NOTE: keep this file ASCII-only. cmd.exe reads .bat files with the console
rem code page (936 on Chinese Windows), so UTF-8 Chinese in comments can be
rem split mid-character and executed as a command, printing spurious errors.

set "REPO=LzYita/RFERP"
set "KEY=%USERPROFILE%\.rferp\update-signing.key"

set "VERSION=%~1"
if "%VERSION%"=="" set /p "VERSION=Version (e.g. 1.2.3): "
if "%VERSION%"=="" (
    echo No version given.
    pause
    exit /b 1
)

set "NOTES_FILE=%~2"
if "%NOTES_FILE%"=="" set "NOTES_FILE=release-notes.txt"

set "TAG=v%VERSION%"
set "ASSET_BASE=https://github.com/%REPO%/releases/download/%TAG%"

rem Accelerator prefix for the update-package download URL (helps when GitHub
rem is slow or unstable). Leave empty to use the plain GitHub URL; can also be
rem overridden with the MIRROR environment variable.
if not defined MIRROR set "MIRROR=https://ghfast.top/"
set "PKG_URL=%MIRROR%%ASSET_BASE%/RFERP-%VERSION%.zip"

echo ================================
echo   Release RFERP %VERSION%
echo ================================

echo [1/7] Check signing key ...
if not exist "%KEY%" (
    echo   Missing private key: %KEY%
    pause
    exit /b 1
)

echo [2/7] Release notes ...
if not exist "!NOTES_FILE!" (
    > "!NOTES_FILE!" echo RFERP %VERSION%
    >> "!NOTES_FILE!" echo.
    >> "!NOTES_FILE!" echo - 
    echo   Created !NOTES_FILE!. Please edit it, then run release.bat again.
    start "" notepad "!NOTES_FILE!"
    pause
    exit /b 1
)
echo   ---- release notes ----
type "!NOTES_FILE!"
echo   -----------------------

echo [3/7] Build RFERP.exe ...
go build -ldflags="-linkmode=internal -H windowsgui -X main.version=%VERSION%" -o RFERP.exe cmd/desktop/main.go
if errorlevel 1 (
    echo   Build failed.
    pause
    exit /b 1
)
if exist rcedit-x64.exe if exist picture\app.ico rcedit-x64.exe RFERP.exe --set-icon picture\app.ico >nul

echo [4/7] Package update zip ...
if not exist dist mkdir dist
if exist "dist\RFERP-%VERSION%.zip" del "dist\RFERP-%VERSION%.zip"
powershell -NoProfile -Command "Compress-Archive -Path 'RFERP.exe' -DestinationPath 'dist\RFERP-%VERSION%.zip' -Force"
if errorlevel 1 (
    echo   Package failed.
    pause
    exit /b 1
)

echo [5/7] Sign manifest ...
go run ./cmd/signmanifest -key "%KEY%" -zip "dist\RFERP-%VERSION%.zip" -url "%PKG_URL%" -version "%VERSION%" -notes-file "!NOTES_FILE!" -out "dist\releases.json"
if errorlevel 1 (
    echo   Signing failed.
    pause
    exit /b 1
)

echo [6/7] Build installer ...
if not defined ISCC (
    echo   ISCC not found. Install Inno Setup 6 or set ISCC.
    pause
    exit /b 1
)
if not exist "!ISCC!" (
    echo   ISCC not found at "!ISCC!".
    pause
    exit /b 1
)
"!ISCC!" /DMyAppVersion=%VERSION% setup.iss >nul
if errorlevel 1 (
    echo   Installer build failed.
    pause
    exit /b 1
)

echo [7/7] Upload to GitHub Release ...
if not defined GH (
    echo   gh not found in PATH. Install GitHub CLI or set GH.
    pause
    exit /b 1
)
"!GH!" auth status >nul 2>nul
if errorlevel 1 (
    echo   gh not logged in. Run: gh auth login
    pause
    exit /b 1
)
rem Assemble the release body: hand-written highlights + the PR list GitHub
rem generates from .github/release.yml + commits pushed straight to main.
rem The result goes to release-notes.full.md (gitignored); the hand-written
rem highlights are still what the update manifest (signmanifest) receives.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0changelog.ps1" -Version %VERSION% -Repo %REPO% -Highlights "!NOTES_FILE!" -Out "release-notes.full.md"
if errorlevel 1 (
    echo   Changelog generation failed.
    pause
    exit /b 1
)
"!GH!" release create %TAG% --draft --target main --title "RFERP %VERSION%" --notes-file "release-notes.full.md" "dist\Setup-RFERP-%VERSION%.exe" "dist\RFERP-%VERSION%.zip" "dist\releases.json"
if errorlevel 1 (
    echo   Release create failed.
    pause
    exit /b 1
)
"!GH!" release edit %TAG% --draft=false --latest
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
