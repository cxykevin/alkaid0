#!/usr/bin/env pwsh
# Alkaid0 Windows 自动安装脚本（MSIX）
#requires -RunAsAdministrator

# 颜色定义
$RED = 'Red'
$GREEN = 'Green'
$YELLOW = 'Yellow'
$BLUE = 'Cyan'

function Write-LogMain {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor $GREEN
}

function Write-LogSub {
    param([string]$Message)
    Write-Host "  --> $Message" -ForegroundColor $BLUE
}

function Write-LogSubWarn {
    param([string]$Message)
    Write-Host "  --> $Message" -ForegroundColor $YELLOW
}

function Write-LogWarn {
    param([string]$Message)
    Write-Host "==> 警告: $Message" -ForegroundColor $YELLOW
}

function Write-LogError {
    param([string]$Message)
    Write-Host "==> 错误: $Message" -ForegroundColor $RED
    exit 1
}

function Print-Logo {
    @"
[0m       [47m  [0m [47m  [0m            [46m [0m[46m [0m     [47m  [0m       
       [47m  [0m [47m  [0m                   [47m  [0m [47m      [0m
[47m[8malkaid[0m[8m0[47m  [0m [47m  [0m  [47m  [0m [47m      [0m [47m  [0m [47m   [0m [47m  [0m [47m  [0m  [47m  [0m
[47m  [0m  [47m  [0m [47m  [0m [47m    [0m   [47m  [0m  [47m  [0m [47m  [0m [47m  [0m  [47m  [0m [47m  [0m  [47m  [0m
[47m   [0m [47m     [0m [47m  [0m  [47m  [0m [47m   [0m [47m     [0m [47m      [0m [47m      [0m
[0m  [2m╭────────────────────────────────╮[0m
[0m  [2m│ [0m[1;34malkaid0[0m[2m coding agent installer │[0m
[0m  [2m╰────────────────────────────────╯[0m
"@
}

function Detect-Arch {
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($arch -eq 'AMD64') {
        return 'amd64'
    } elseif ($arch -eq 'ARM64') {
        Write-LogSubWarn "ARM64 架构将使用 amd64 MSIX 包"
        return 'amd64'
    } else {
        Write-LogError "不支持的架构: $arch"
    }
}

function Get-LatestRelease {
    $apiUrl = 'https://api.github.com/repos/cxykevin/alkaid0/releases'
    $maxRetries = 3
    $retryDelay = 5
    $attempt = 1

    while ($attempt -le $maxRetries) {
        try {
            $response = Invoke-RestMethod -Uri $apiUrl -Method Get -TimeoutSec 30 -ErrorAction Stop
            if ($response) {
                $tag = $response[0].tag_name
                if ($tag) {
                    return $tag
                } else {
                    Write-LogSubWarn "未找到有效的 Release tag (尝试 $attempt/$maxRetries)"
                }
            } else {
                Write-LogSubWarn "API 返回空响应 (尝试 $attempt/$maxRetries)"
            }
        } catch {
            Write-LogSubWarn "请求失败 (尝试 $attempt/$maxRetries): $($_.Exception.Message)"
        }

        if ($attempt -lt $maxRetries) {
            Start-Sleep -Seconds $retryDelay
        }
        $attempt++
    }
    Write-LogError "获取最新 Release 失败，已重试 $maxRetries 次"
}

function Install-MSIX {
    param([string]$PackagePath)

    Write-LogSub "安装 MSIX 包: $PackagePath"
    
    # 尝试使用 -AllowUnsigned -Trust（如果 PowerShell 版本支持）
    $params = @{
        Path = $PackagePath
        ErrorAction = 'Stop'
    }
    # 在较新 PowerShell 中，-Trust 可能不存在；使用 -AllowUnsigned 和 -Trust 可以共存
    try {
        Add-AppxPackage @params -AllowUnsigned -Trust -ErrorAction Stop
        Write-LogSub "安装成功"
        return
    } catch {
        Write-LogSubWarn "使用 -AllowUnsigned -Trust 失败: $($_.Exception.Message)"
        Write-LogSubWarn "尝试使用普通安装（可能需要手动确认）..."
    }

    # 回退：不带 -AllowUnsigned -Trust
    try {
        Add-AppxPackage @params -ErrorAction Stop -AllowUnsigned
        Write-LogSub "安装成功"
    } catch {
        Write-LogError "使用 -AllowUnsigned 安装失败: $($_.Exception.Message)"
    }
    try {
        Add-AppxPackage @params -ErrorAction Stop
        Write-LogSub "安装成功"
    } catch {
        Write-LogError "安装失败: $($_.Exception.Message)"
    }
}

function Main {
    Print-Logo
    Write-LogMain "Alkaid0 安装脚本 (Windows)"
    
    $ARCH = Detect-Arch
    Write-LogSub "架构: $ARCH"
    
    Write-LogMain "获取最新 Release..."
    $TAG = Get-LatestRelease
    Write-LogSub "最新版本: $TAG"
    
    $package = "alkaid0-windows-amd64.msix"
    $downloadUrl = "https://github.com/cxykevin/alkaid0/releases/download/$TAG/$package"
    Write-LogSub "安装包: $package"
    
    $tempDir = Join-Path $env:TEMP "alkaid0_install"
    New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
    $localPath = Join-Path $tempDir $package
    
    Write-LogMain "下载安装包"
    Write-LogSub "下载: $downloadUrl"
    try {
        Invoke-WebRequest -Uri $downloadUrl -OutFile $localPath -TimeoutSec 120 -ErrorAction Stop
    } catch {
        Write-LogError "下载失败: $($_.Exception.Message)"
    }
    
    Write-LogMain "安装 MSIX"
    Install-MSIX -PackagePath $localPath
    
    Remove-Item -Recurse -Force $tempDir -ErrorAction SilentlyContinue
    
    Write-LogMain "安装完成!"
    Write-LogSub "应用程序已安装，请从开始菜单启动 alkaid0"
}

Main