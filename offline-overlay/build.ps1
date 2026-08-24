$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Invoke-CheckedCommand {
	param(
		[Parameter(Mandatory = $true)]
		[string]$Name,
		[Parameter(Mandatory = $true)]
		[scriptblock]$Command
	)

	& $Command
	if ($LASTEXITCODE -ne 0) {
		throw "$Name failed: exit code $LASTEXITCODE"
	}
}

$project = Split-Path -Parent $PSScriptRoot
$release = Join-Path $project "dist\release"
$pwaRelease = Join-Path $project "dist\pwa"
$resource = Join-Path $project "cmd\tasknotifier\rsrc_windows_amd64.syso"
$vendorDirectory = Join-Path $project "vendor"
$versionInfoExe = Join-Path $project "tools\bin\goversioninfo.exe"
$timeZone = [System.TimeZoneInfo]::FindSystemTimeZoneById("Tokyo Standard Time")
$buildTime = [System.TimeZoneInfo]::ConvertTimeFromUtc([DateTime]::UtcNow, $timeZone)
$buildTimestamp = $buildTime.ToString("yyyyMMddHHmm")
$displayVersion = "v0.0.1.$buildTimestamp"

if (-not (Test-Path $vendorDirectory)) {
	throw "vendor directory was not found. Use the offline-source package that includes vendor/."
}
if (-not (Test-Path $versionInfoExe)) {
	throw "tools\bin\goversioninfo.exe was not found. Use the offline-source package."
}

$oldGoProxy = $env:GOPROXY
$oldGoSumDb = $env:GOSUMDB
$oldGoFlags = $env:GOFLAGS
$env:GOPROXY = "off"
$env:GOSUMDB = "off"
$env:GOFLAGS = "-mod=vendor"

try {
	Set-Location $project

	if (Test-Path $pwaRelease) {
		Remove-Item -Recurse -Force $pwaRelease
	}

	Invoke-CheckedCommand "genicon" { go run ./tools/genicon --source resources/app.svg --out resources/app.ico --pwa-dir web/pwa/icons }
	Invoke-CheckedCommand "webbuild" { go run ./tools/webbuild }
	Invoke-CheckedCommand "goversioninfo" {
		& $versionInfoExe `
			-64 `
			-o $resource `
			-icon resources/app.ico `
			-application-icon resources/app.ico `
			-manifest resources/app.manifest `
			-file-version $displayVersion `
			-product-version $displayVersion `
			resources/versioninfo.json
	}

	if (Test-Path $release) {
		Remove-Item -Recurse -Force $release
	}
	New-Item -ItemType Directory -Force $release | Out-Null

	$exePath = Join-Path $release "TaskNotifier.exe"
	$buildInfoPath = Join-Path $release "BUILD-INFO.txt"
	Invoke-CheckedCommand "TaskNotifier build" {
		go build -buildvcs=false -trimpath -ldflags "-s -w -H=windowsgui -X tasknotifier/internal/tasknotifier.BuildVersion=$displayVersion" -o $exePath ./cmd/tasknotifier
	}
	Invoke-CheckedCommand "buildinfo" {
		go run ./tools/buildinfo `
			--version $displayVersion `
			--exe $exePath `
			--changes resources/changes.txt `
			--output $buildInfoPath
	}

	(Get-Item $exePath).LastWriteTime = $buildTime
	(Get-Item $buildInfoPath).LastWriteTime = $buildTime

	& (Join-Path $PSScriptRoot "verify-release.ps1") -ReleaseDirectory $release

	$packageDirectory = Join-Path $project "dist\package"
	if (Test-Path $packageDirectory) {
		Remove-Item -Recurse -Force $packageDirectory
	}
	$exePackage = Join-Path $packageDirectory "exe"
	$pwaPackage = Join-Path $packageDirectory "pwa"
	New-Item -ItemType Directory -Force $exePackage | Out-Null
	New-Item -ItemType Directory -Force $pwaPackage | Out-Null
	Copy-Item (Join-Path $release "*") $exePackage -Recurse
	Copy-Item (Join-Path $pwaRelease "*") $pwaPackage -Recurse

	$zip = Join-Path $project "dist\TaskNotifier-$displayVersion.zip"
	if (Test-Path $zip) {
		Remove-Item -Force $zip
	}
	Compress-Archive -Path (Join-Path $packageDirectory "*") -DestinationPath $zip
	Write-Host "Created $zip ($displayVersion, EXE + PWA, offline build)"
}
finally {
	$env:GOPROXY = $oldGoProxy
	$env:GOSUMDB = $oldGoSumDb
	$env:GOFLAGS = $oldGoFlags
}
