@echo off
X:\winkit\winkit-service.exe run-user --user {{USER_NAME}} --password {{USER_PASSWORD}} --current-dir E:\ -- "E:\Program Files\WSL\wsl.exe" %*
exit /b %ERRORLEVEL%
