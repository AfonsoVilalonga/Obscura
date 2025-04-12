@echo off

echo Starting Node.js server...
start cmd /k "node Node-Server/index.js"
if %errorlevel% neq 0 (
    echo Failed to start Node.js server.
    exit /b %errorlevel%
)

timeout /t 5 /nobreak >nul

echo Launching Google Chrome at localhost:3010...
start chrome --headless --disable-gpu --no-sandbox --autoplay-policy=no-user-gesture-required --user-data-dir=C:\Chrome\user --mute-audio --ignore-certificate-errors http://localhost:3010
if %errorlevel% neq 0 (
    echo Failed to launch Google Chrome.
    exit /b %errorlevel%
)

echo Done.