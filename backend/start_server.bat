@echo off
set JWT_SECRET=dev-jwt-secret-please-change-in-production-32chars
set ENCRYPTION_KEY=dev-encryption-key-please-change-32bytes-long
set PORT=5176
cd /d E:\开发项目\workspace\backend
start server.exe
