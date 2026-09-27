# dev-watch.ps1 - 后端文件监听 + 自动编译重启
# 用法: .\dev-watch.ps1

$backendDir = "E:\开发项目\workspace\backend"
$serverExe = Join-Path $backendDir "server.exe"
$goBin = "C:\Program Files\Go\bin\go.exe"

function Start-Server {
    Write-Host "[RESTART] 启动服务..." -ForegroundColor Yellow
    # 杀掉旧进程
    $pids = Get-NetTCPConnection -LocalPort 5176 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique
    foreach ($pid in $pids) {
        if ($pid -gt 0) {
            try { Stop-Process -Id $pid -Force -ErrorAction SilentlyContinue } catch {}
        }
    }
    Start-Sleep -Milliseconds 500

    $proc = Start-Process -FilePath $serverExe -WorkingDirectory $backendDir -PassThru -NoNewWindow
    Write-Host "[OK] 服务已启动 PID=$($proc.Id)" -ForegroundColor Green
    return $proc
}

function Build-Server {
    Write-Host "[BUILD] 编译中..." -ForegroundColor Cyan
    & $goBin build -o $serverExe ./cmd/server
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[OK] 编译成功" -ForegroundColor Green
        return $true
    } else {
        Write-Host "[FAIL] 编译失败" -ForegroundColor Red
        return $false
    }
}

Write-Host "=== AI Router Gateway 热重载服务 ===" -ForegroundColor Magenta
Write-Host "监听目录: $backendDir" -ForegroundColor White

# 初次编译 + 启动
if (-not (Build-Server)) { exit 1 }
$server = Start-Server

# 文件监听
$watcher = New-Object System.IO.FileSystemWatcher
$watcher.Path = $backendDir
$watcher.IncludeSubdirectories = $true
$watcher.Filter = "*.go"
$watcher.NotifyFilter = [System.IO.NotifyFilters]::LastWrite -bor [System.IO.NotifyFilters]::FileName

$watcher.EnableRaisingEvents = $true

$triggered = $false
$action = [System.IO.FileSystemEventHandler] {
    param($source, $e)
    if ($triggered) { return }
    $triggered = $true

    Write-Host "`n[FILE] $($e.ChangeType): $($e.FullPath)" -ForegroundColor Yellow
    Write-Host "[FILE] 500ms 后自动重启..." -ForegroundColor Gray

    # 延迟执行避免编辑器保存时连续触发
    Start-Sleep -Milliseconds 500

    if (Build-Server) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 500
        $server = Start-Server
    }

    $triggered = $false
}

Register-ObjectEvent -InputObject $watcher -EventName Changed -Action $action | Out-Null
Register-ObjectEvent -InputObject $watcher -EventName Created -Action $action | Out-Null

Write-Host ""
Write-Host "按 Ctrl+C 停止" -ForegroundColor Yellow
Write-Host "正在监听文件变化... (修改 .go 文件后 500ms 自动重启)" -ForegroundColor Green

# 等待 Ctrl+C
try {
    $server.WaitForExit()
} catch {
    # Ctrl+C
} finally {
    Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    $watcher.Dispose()
    Write-Host "`n[STOP] 服务已停止" -ForegroundColor Red
}
