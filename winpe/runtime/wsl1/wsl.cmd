@echo off
X:\winkit\pwsh\pwsh.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File X:\winkit\invoke-wsl.ps1 %*
exit /b %ERRORLEVEL%
