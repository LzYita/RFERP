@echo off
cd /d D:\opencode\sql_project
copy D:\TDM-GCC\bin\libgcc_s_seh-1.dll /y >nul 2>nul
copy D:\TDM-GCC\bin\libstdc++-6.dll /y >nul 2>nul
copy D:\TDM-GCC\bin\libwinpthread-1.dll /y >nul 2>nul
start RFERP.exe
pause
