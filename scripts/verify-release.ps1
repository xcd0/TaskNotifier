param(
	[Parameter(Mandatory = $true)]
	[string]$ReleaseDirectory
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$release = (Resolve-Path $ReleaseDirectory).Path
$names = @(Get-ChildItem -File $release | Sort-Object Name | ForEach-Object Name)
$expected = @("BUILD-INFO.txt", "TaskNotifier.exe")
if (Compare-Object $expected $names) {
	throw "Release directory must contain exactly BUILD-INFO.txt and TaskNotifier.exe: $($names -join ', ')"
}

$exePath = Join-Path $release "TaskNotifier.exe"
$exeBytes = [System.IO.File]::ReadAllBytes($exePath)
$exeASCII = [System.Text.Encoding]::ASCII.GetString($exeBytes)
if ($exeASCII -notmatch "CreateCoreWebView2Controller") {
	throw "TaskNotifier.exe does not contain the embedded WebView2 integration"
}
$peOffset = [BitConverter]::ToInt32($exeBytes, 0x3C)
$optionalHeader = $peOffset + 4 + 20
$subsystem = [BitConverter]::ToUInt16($exeBytes, $optionalHeader + 68)
if ($subsystem -ne 2) {
	throw "TaskNotifier.exe is not a Windows GUI subsystem executable"
}

$versionInfo = [System.Diagnostics.FileVersionInfo]::GetVersionInfo($exePath)
$versionPattern = '^v0\.0\.1\.\d{12}$'
if ($versionInfo.FileVersion -notmatch $versionPattern) {
	throw "TaskNotifier.exe has an invalid FileVersion: $($versionInfo.FileVersion)"
}
if ($versionInfo.ProductVersion -notmatch $versionPattern) {
	throw "TaskNotifier.exe has an invalid ProductVersion: $($versionInfo.ProductVersion)"
}
if ($versionInfo.FileDescription -ne "TaskNotifier" -or $versionInfo.ProductName -ne "TaskNotifier") {
	throw "TaskNotifier.exe has incomplete version information"
}

$buildInfoPath = Join-Path $release "BUILD-INFO.txt"
$buildInfoBytes = [System.IO.File]::ReadAllBytes($buildInfoPath)
if ($buildInfoBytes.Length -ge 3 -and $buildInfoBytes[0] -eq 0xEF -and $buildInfoBytes[1] -eq 0xBB -and $buildInfoBytes[2] -eq 0xBF) {
	throw "BUILD-INFO.txt contains a UTF-8 BOM"
}
$buildInfo = Get-Content -Raw -Encoding UTF8 $buildInfoPath
$exeHash = (Get-FileHash $exePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($buildInfo -notmatch [regex]::Escape("バージョン: $($versionInfo.FileVersion)")) {
	throw "BUILD-INFO.txt does not contain the EXE version"
}
if ($buildInfo -notmatch [regex]::Escape("EXE SHA-256: $exeHash")) {
	throw "BUILD-INFO.txt does not contain the EXE SHA-256"
}
if ($buildInfo -notmatch "変更点:") {
	throw "BUILD-INFO.txt does not contain changes"
}

Add-Type -AssemblyName System.Drawing
$icon = [System.Drawing.Icon]::ExtractAssociatedIcon($exePath)
if ($null -eq $icon) {
	throw "TaskNotifier.exe has no associated icon"
}
$icon.Dispose()

$kitsRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
$mt = Get-ChildItem $kitsRoot -Filter mt.exe -Recurse | Sort-Object FullName -Descending | Select-Object -First 1
if ($null -eq $mt) {
	throw "mt.exe was not found"
}
$manifestPath = Join-Path $env:TEMP "TaskNotifier.extracted.manifest"
& $mt.FullName "-inputresource:$exePath;#1" "-out:$manifestPath"
if ($LASTEXITCODE -ne 0) {
	throw "Failed to extract embedded manifest"
}
$manifest = Get-Content -Raw $manifestPath
if ($manifest -notmatch "PerMonitorV2") {
	throw "Embedded manifest does not contain PerMonitorV2"
}
Remove-Item -Force $manifestPath

$project = Split-Path -Parent $PSScriptRoot
$forbidden = Get-ChildItem $project -Filter *.go -Recurse | Select-String -Pattern "schtasks|New-ScheduledTask|Register-ScheduledTask|taskschd"
if ($forbidden) {
	throw "Windows Task Scheduler reference found: $forbidden"
}

$localServer = Get-ChildItem $project -Filter *.go -Recurse | Select-String -Pattern '"net/http"|ListenAndServe|httptest.NewServer'
if ($localServer) {
	throw "Local HTTP server reference found: $localServer"
}

$webOutput = Join-Path $project "internal\tasknotifier\webui_dist\index.html"
$webText = Get-Content -Raw -Encoding UTF8 $webOutput
if ($webText -match '/\* TASKNOTIFIER_(STYLE|SCRIPT) \*/' -or $webText -match 'https?://') {
	throw "Embedded Web UI contains an unexpanded marker or external URL"
}

Write-Host "Release verification passed: $($versionInfo.FileVersion)"
