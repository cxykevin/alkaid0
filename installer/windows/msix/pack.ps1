Write-Output "==> Init"
Remove-Item -Path "dist" -Recurse -Force -ErrorAction SilentlyContinue
New-Item "dist" -ItemType Directory
New-Item "dist\Bin" -ItemType Directory
Remove-Item -Path "Assets" -Recurse -Force -ErrorAction SilentlyContinue
New-Item "Assets" -ItemType Directory

Write-Output "==> Get Git Info"
$tag = git describe --tags --abbrev=0 2>$null
if (-not $tag) {
    Write-Output "  --> No tags found in this repository."
    $tag = "v0.0.0"
} 
$version = $tag -replace '^v', ''
Write-Output "  --> Git Tag Version: ${version}"

Write-Output "==> Generate Icons"
& "C:\Program Files\cim\bin\cim.exe" png "..\..\..\logo\icon16x16d.svg" "Assets\AppList.png" -w 44 -h 44
& "C:\Program Files\cim\bin\cim.exe" png "..\..\..\logo\icon16x16d.svg" "Assets\AppList.scale-200.png" -w 88 -h 88
& "C:\Program Files\cim\bin\cim.exe" png "..\..\..\logo\icon16x16d.svg" "Assets\AppList.targetsize-24_altform-unplated.png" -w 24 -h 24
& "C:\Program Files\cim\bin\cim.exe" png "..\..\..\logo\icon16x16d.svg" "Assets\MedTile.png" -w 150 -h 150
& "C:\Program Files\cim\bin\cim.exe" png "..\..\..\logo\icon16x16d.svg" "Assets\MedTile.scale-200.png" -w 300 -h 300
& "C:\Program Files\cim\bin\cim.exe" png "..\..\..\logo\icon16x16d.svg" "Assets\StoreLogo.png" -w 50 -h 50

Write-Output "==> Copy Binary"
Copy-Item ".\alkaid0.exe" ".\dist\Bin\alkaid0.exe"

Write-Output "==> Get Cert"
$CertFile = ".cert.tmp"
Write-Output "  --> Reading Base64 certificate from '$CertFile'..."
$base64 = (Get-Content -Path $CertFile -Raw) -replace '\s', ''
if ([string]::IsNullOrEmpty($base64)) {
    Write-Error "Certificate file is empty or not found."
    exit 1
}
$pfxPath = Join-Path $env:TEMP "temp_cert_$(Get-Random).pfx"
Write-Output "  --> Decoding certificate to '$pfxPath'..."
try {
    [IO.File]::WriteAllBytes($pfxPath, [Convert]::FromBase64String($base64))
}
catch {
    Write-Error "  --> Failed to decode Base64 certificate: $_"
    exit 1
}

Write-Output "==> Generate Template"
$template = "Package.template.appxmanifest"
$target = "Package.appxmanifest"
# 检查模板是否存在
if (-not (Test-Path $template)) {
    Write-Error "  --> Template file '$template' not found."
    exit 1
}
$content = Get-Content $template -Raw
$content = $content -replace '{{version}}', $version
$content | Set-Content $target -NoNewline
Write-Output "  --> Created $target with version $version"

Write-Output "==> Build Service Support"
g++.exe ..\service\alkservice.cpp -o .\dist\Bin\alkservice.exe -mwindows -static -ladvapi32 -luser32

Write-Output "==> Packing"
winapp.exe package ".\dist" --cert "$pfxPath" --output=".\alkaid0-windows-amd64.msix"

Write-Output "==> Build Finished!"
