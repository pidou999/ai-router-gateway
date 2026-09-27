@echo off
set JWT_SECRET=your-jwt-secret-change-in-production-2026-secure-key
set ENCRYPTION_KEY=dev-encryption-key-please-change-32bytes-long
set DB_TYPE=sqlite
set DB_PATH=E:\开发项目\workspace\backend\data\gateway.db
set PORT=5176
set GIN_MODE=release
cd /d E:\开发项目\workspace\backend
gateway.exe
