@echo off
set JWT_SECRET=dev-jwt-secret-please-change-in-production-32chars
set ENCRYPTION_KEY=dev-encryption-key-please-change-32bytes-long
set DB_PATH=E:\开发项目\workspace\backend\data\gateway.db
set PORT=5176
E:\开发项目\workspace\backend\server.exe
pause
