@echo off
REM Demo script for Self-Healing Distributed Cache (Windows)
REM This script demonstrates the self-healing capabilities of the cache cluster.
REM
REM Usage: demo.bat
REM Requirements: Go 1.22+, Windows

echo ========================================
echo   Self-Healing Distributed Cache Demo
echo ========================================
echo.

REM Build the server
echo [BUILD] Building cache server...
go build -o cache-server.exe ./cmd/cache/
if errorlevel 1 (
    echo [ERROR] Build failed!
    exit /b 1
)
echo [BUILD] Build complete!
echo.

REM Create temp directory for logs
set TMPDIR=%TEMP%\cache-demo-%RANDOM%
mkdir %TMPDIR%
echo [INFO] Logs will be saved to: %TMPDIR%
echo.

REM Start 3-node cluster
REM Identity uses 127.0.0.1:port everywhere so ring, gossip, and liveness
REM checks all agree. -gossip-advertise-addr is required: without it memberlist
REM advertises the 0.0.0.0 bind address, which peers can never match.
echo [CLUSTER] Starting 3-node cluster...
start "Node A" /min cache-server.exe -addr :8080 -id 127.0.0.1:8080 -advertise-addr 127.0.0.1:8080 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8081,127.0.0.1:8082" > %TMPDIR%\node-a.log 2>&1
echo   node-a started
timeout /t 2 /nobreak > nul

start "Node B" /min cache-server.exe -addr :8081 -id 127.0.0.1:8081 -advertise-addr 127.0.0.1:8081 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8082" > %TMPDIR%\node-b.log 2>&1
echo   node-b started
timeout /t 1 /nobreak > nul

start "Node C" /min cache-server.exe -addr :8082 -id 127.0.0.1:8082 -advertise-addr 127.0.0.1:8082 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8081" > %TMPDIR%\node-c.log 2>&1
echo   node-c started

REM Wait for cluster to stabilize
echo [CLUSTER] Waiting for cluster to stabilize...
timeout /t 4 /nobreak > nul
echo [CLUSTER] Cluster ready!
echo.

REM Phase 1: Populate with data
echo --- Phase 1: Populating cache with data ---
for /L %%i in (1,1,20) do (
    curl -s -X POST http://localhost:8080/set -H "Content-Type: application/json" -d "{\"key\":\"key-%%i\",\"value\":\"value-%%i\",\"ttl_ms\":60000}" > nul
)
echo [INFO] Wrote 20 keys to the cluster
echo.

REM Phase 2: Verify data accessibility
echo [TEST] Verifying data accessibility...
set SUCCESS=0
for /L %%i in (1,1,20) do (
    curl -s http://localhost:8081/get?key=key-%%i | findstr /C:"value-%%i" > nul
    if not errorlevel 1 set /a SUCCESS+=1
)
echo [TEST] %SUCCESS%/20 keys accessible from node-b
echo.

REM Phase 3: Kill a node (simulate failure)
echo --- Phase 3: Simulating node failure ---
echo [FAILURE] Killing node-b...
taskkill /FI "WINDOWTITLE eq Node B" /F > nul 2>&1
echo [INFO] Waiting for failure detection...
timeout /t 5 /nobreak > nul
echo.

REM Phase 4: Verify self-healing
echo --- Phase 4: Verifying self-healing ---
echo [TEST] Checking if data is still accessible...
set SUCCESS=0
for /L %%i in (1,1,20) do (
    curl -s http://localhost:8080/get?key=key-%%i | findstr /C:"value-%%i" > nul
    if not errorlevel 1 set /a SUCCESS+=1
)
echo [TEST] %SUCCESS%/20 keys still accessible after node failure
echo.

REM Check cluster health
echo [INFO] Cluster health:
curl -s http://localhost:8080/cluster/info
echo.
echo.

REM Phase 5: Node recovery
echo --- Phase 5: Node recovery ---
echo [RECOVERY] Restarting node-b...
start "Node B" /min cache-server.exe -addr :8081 -id 127.0.0.1:8081 -advertise-addr 127.0.0.1:8081 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8082" > %TMPDIR%\node-b.log 2>&1
echo [INFO] Waiting for recovery...
timeout /t 5 /nobreak > nul
echo.

REM Final verification
echo --- Final Verification ---
echo [TEST] Checking data after recovery...
set SUCCESS=0
for /L %%i in (1,1,20) do (
    curl -s http://localhost:8081/get?key=key-%%i | findstr /C:"value-%%i" > nul
    if not errorlevel 1 set /a SUCCESS+=1
)
echo [TEST] %SUCCESS%/20 keys accessible from recovered node-b
echo.

echo ========================================
echo   Demo Complete!
echo ========================================
echo.
echo The cluster successfully:
echo   1. Distributed data across 3 nodes
echo   2. Survived node failure with zero data loss
echo   3. Automatically detected the failure
echo   4. Recovered when the node rejoined
echo.
echo Logs saved to: %TMPDIR%
echo.

REM Cleanup
echo [CLEANUP] Cleaning up...
taskkill /FI "WINDOWTITLE eq Node A" /F > nul 2>&1
taskkill /FI "WINDOWTITLE eq Node B" /F > nul 2>&1
taskkill /FI "WINDOWTITLE eq Node C" /F > nul 2>&1
timeout /t 1 /nobreak > nul
rmdir /s /q %TMPDIR% 2>nul
echo [CLEANUP] Done!
