@echo off

echo Starting Node.js server...
start cmd /k "node Node-Server/index.js"
if %errorlevel% neq 0 (
    echo Failed to start Node.js server.
    exit /b %errorlevel%
)

timeout /t 5 /nobreak >nul

echo Launching Google Chrome at localhost:3010...

start firefox http://localhost:3010

echo Done.
