#!/usr/bin/env pwsh
# Alkaid0 Windows ×Ô¶¯°²×°½Å±¾£¨MSIX£©
#requires -RunAsAdministrator

# ÑÕÉ«¶¨Òå
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
    Write-Host "==> ¾¯¸æ: $Message" -ForegroundColor $YELLOW
}

function Write-LogError {
    param([string]$Message)
    Write-Host "==> ´íÎó: $Message" -ForegroundColor $RED
    exit 1
}

function Print-Logo {
    @"
[0m       [47m  [0m [47m  [0m            [46m [0m[46m [0m     [47m  [0m       
       [47m  [0m [47m  [0m                   [47m  [0m [47m      [0m
[47m[8malkaid[0m[8m [47m[8m0 [0m [47m  [0m  [47m  [0m [47m      [0m [47m  [0m [47m   [0m [47m  [0m [47m  [0m  [47m  [0m
[47m  [0m  [47m  [0m [47m  [0m [47m    [0m   [47m  [0m  [47m  [0m [47m  [0m [47m  [0m  [47m  [0m [47m  [0m  [47m  [0m
[47m   [0m [47m     [0m [47m  [0m  [47m  [0m [47m   [0m [47m     [0m [47m      [0m [47m      [0m
[0m  [2m¨q©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤¨r[0m
[0m  [2m©¦ [0m[1;34malkaid0[0m[2m coding agent installer ©¦[0m
[0m  [2m¨t©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤©¤¨s[0m
"@
}

function Detect-Arch {
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($arch -eq 'AMD64') {
        return 'amd64'
    } elseif ($arch -eq 'ARM64') {
        Write-LogSubWarn "ARM64 ¼Ü¹¹½«Ê¹ÓÃ amd64 MSIX °ü"
        return 'amd64'
    } else {
        Write-LogError "²»Ö§³ÖµÄ¼Ü¹¹: $arch"
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
                    Write-LogSubWarn "Î´ÕÒµ½ÓÐÐ§µÄ Release tag (³¢ÊÔ $attempt/$maxRetries)"
                }
            } else {
                Write-LogSubWarn "API ·µ»Ø¿ÕÏìÓ¦ (³¢ÊÔ $attempt/$maxRetries)"
            }
        } catch {
            Write-LogSubWarn "ÇëÇóÊ§°Ü (³¢ÊÔ $attempt/$maxRetries): $($_.Exception.Message)"
        }

        if ($attempt -lt $maxRetries) {
            Start-Sleep -Seconds $retryDelay
        }
        $attempt++
    }
    Write-LogError "»ñÈ¡×îÐÂ Release Ê§°Ü£¬ÒÑÖØÊÔ $maxRetries ´Î"
}

function Install-MSIX {
    param([string]$PackagePath)

    Write-LogSub "°²×°Ö¤Êé: ${PackagePath}.cer"
    
    Import-Certificate -FilePath "${PackagePath}.cer" -CertStoreLocation Cert:\LocalMachine\Root

    Write-LogSub "°²×° MSIX °ü: $PackagePath"
    
    # ³¢ÊÔÊ¹ÓÃ -AllowUnsigned -Trust£¨Èç¹û PowerShell °æ±¾Ö§³Ö£©
    $params = @{
        Path = $PackagePath
        ErrorAction = 'Stop'
    }
    # ÔÚ½ÏÐÂ PowerShell ÖÐ£¬-Trust ¿ÉÄÜ²»´æÔÚ£»Ê¹ÓÃ -AllowUnsigned ºÍ -Trust ¿ÉÒÔ¹²´æ
    try {
        Add-AppxPackage @params -AllowUnsigned
        Write-LogSub "°²×°³É¹¦"
        return
    } catch {
        Write-LogSubWarn "Ê¹ÓÃ -AllowUnsigned Ê§°Ü: $($_.Exception.Message)"
        Write-LogSubWarn "³¢ÊÔÊ¹ÓÃÆÕÍ¨°²×°£¨¿ÉÄÜÐèÒªÊÖ¶¯È·ÈÏ£©..."
    }

    # »ØÍË£º²»´ø -AllowUnsigned -Trust
    try {
        Add-AppxPackage @params 
        Write-LogSub "°²×°³É¹¦"
    } catch {
        Write-LogError "Ê¹ÓÃ -AllowUnsigned °²×°Ê§°Ü: $($_.Exception.Message)"
    }
    try {
        Add-AppxPackage @params -ErrorAction Stop
        Write-LogSub "°²×°³É¹¦"
    } catch {
        Write-LogError "°²×°Ê§°Ü: $($_.Exception.Message)"
    }
}

function Main {
    Print-Logo
    Write-LogMain "Alkaid0 °²×°½Å±¾ (Windows)"
    
    $ARCH = Detect-Arch
    Write-LogSub "¼Ü¹¹: $ARCH"
    
    Write-LogMain "»ñÈ¡×îÐÂ Release..."
    $TAG = Get-LatestRelease
    Write-LogSub "×îÐÂ°æ±¾: $TAG"
    
    $package = "alkaid0-windows-amd64.msix"
    $downloadUrl = "https://github.com/cxykevin/alkaid0/releases/download/$TAG/$package"
    $downloadCrtUrl = "https://raw.githubusercontent.com/cxykevin/.pubkey/refs/heads/main/msix-software.cer"
    Write-LogSub "°²×°°ü: $package"
    
    $tempDir = Join-Path $env:TEMP "alkaid0_install"
    New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
    $localPath = Join-Path $tempDir $package
    
    Write-LogMain "ÏÂÔØ°²×°°ü"
    try {
        Write-LogSub "ÏÂÔØ: $downloadCrtUrl "
        Invoke-WebRequest -Uri $downloadCrtUrl -OutFile "${localPath}.cer" -TimeoutSec 60 -ErrorAction Stop -UseBasicParsing
        Write-LogSub "ÏÂÔØ: $downloadUrl"
        Invoke-WebRequest -Uri $downloadUrl -OutFile $localPath -TimeoutSec 180 -ErrorAction Stop -UseBasicParsing
    } catch {
        Write-LogError "ÏÂÔØÊ§°Ü: $($_.Exception.Message)"
    }
    
    Write-LogMain "°²×° MSIX"
    Install-MSIX -PackagePath $localPath
    
    Remove-Item -Recurse -Force $tempDir -ErrorAction SilentlyContinue
    
    Write-LogMain "°²×°Íê³É!"
    Write-LogSub "Ó¦ÓÃ³ÌÐòÒÑ°²×°£¬Çë´Ó¿ªÊ¼²Ëµ¥Æô¶¯ alkaid0"
}

Main