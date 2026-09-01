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

function Restore-GeneratedWebUI {
	param(
		[Parameter(Mandatory = $true)]
		[string]$Project
	)

	$parts = Get-ChildItem (Join-Path $Project "ci-generated\webview.part*") | Sort-Object Name
	if ($parts.Count -ne 3) {
		throw "Expected 3 generated WebView2 chunks, found $($parts.Count)."
	}

	$encoded = (($parts | ForEach-Object { (Get-Content -Raw $_.FullName).Trim() }) -join "")
	$compressed = [Convert]::FromBase64String($encoded)
	$input = New-Object IO.MemoryStream(,$compressed)
	$gzip = New-Object IO.Compression.GZipStream($input, [IO.Compression.CompressionMode]::Decompress)
	$output = New-Object IO.MemoryStream
	$gzip.CopyTo($output)
	$gzip.Dispose()
	$input.Dispose()
	$webViewBytes = $output.ToArray()
	$output.Dispose()

	$webViewPath = Join-Path $Project "internal\tasknotifier\webui_dist\index.html"
	[IO.Directory]::CreateDirectory((Split-Path -Parent $webViewPath)) | Out-Null
	[IO.File]::WriteAllBytes($webViewPath, $webViewBytes)

	$webViewHash = (Get-FileHash $webViewPath -Algorithm SHA256).Hash.ToLowerInvariant()
	if ($webViewHash -ne "e65606a305123f3cb0c735b7193b28590edd1a422afb361aebfbbc71847f85d7") {
		throw "Generated WebView2 HTML hash mismatch: $webViewHash"
	}

	$utf8 = New-Object Text.UTF8Encoding($false)
	$html = [IO.File]::ReadAllText($webViewPath, $utf8)
	$needle = "<title>TaskNotifier</title>`n`t"
	$insertion = '<title>TaskNotifier</title>' + "`n`t" + '<link rel="manifest" href="./manifest.webmanifest"><meta name="theme-color" content="#ffffff"><link rel="icon" href="./icons/app-192.png">'
	if (-not $html.Contains($needle)) {
		throw "PWA insertion point was not found."
	}
	$pwaHtml = $html.Replace($needle, $insertion)

	$pwaRelease = Join-Path $Project "dist\pwa"
	if (Test-Path $pwaRelease) {
		Remove-Item -Recurse -Force $pwaRelease
	}
	New-Item -ItemType Directory -Force $pwaRelease | Out-Null
	[IO.File]::WriteAllText((Join-Path $pwaRelease "index.html"), $pwaHtml, $utf8)

	$pwaHash = (Get-FileHash (Join-Path $pwaRelease "index.html") -Algorithm SHA256).Hash.ToLowerInvariant()
	if ($pwaHash -ne "cb7d29b1078bb63a1d251ceb9538c4ebd843d546e6b02fec2d7989ca8737e8ea") {
		throw "Generated PWA HTML hash mismatch: $pwaHash"
	}

	return $pwaRelease
}

$project = Split-Path -Parent $PSScriptRoot
$release = Join-Path $project "dist\release"
$resource = Join-Path $project "cmd\tasknotifier\rsrc_windows_amd64.syso"
$vendorDirectory = Join-Path $project "vendor"
$versionInfoExe = Join-Path $project "tools\bin\goversioninfo.exe"
$timeZone = [System.TimeZoneInfo]::FindSystemTimeZoneById("Tokyo Standard Time")
$buildTime = [System.TimeZoneInfo]::ConvertTimeFromUtc([DateTime]::UtcNow, $timeZone)
$buildTimestamp = $buildTime.ToString("yyyyMMddHHmm")
$displayVersion = "v0.0.1.$buildTimestamp"

if (-not (Test-Path $vendorDirectory)) {
	throw "vendor directory was not found."
}
if (-not (Test-Path $versionInfoExe)) {
	throw "tools\bin\goversioninfo.exe was not found."
}

$oldGoProxy = $env:GOPROXY
$oldGoSumDb = $env:GOSUMDB
$oldGoFlags = $env:GOFLAGS
$env:GOPROXY = "off"
$env:GOSUMDB = "off"
$env:GOFLAGS = "-mod=vendor"

try {
	Set-Location $project
	$pwaRelease = Restore-GeneratedWebUI -Project $project

	Invoke-CheckedCommand "genicon" { go run ./tools/genicon --source resources/app.svg --out resources/app.ico --pwa-dir web/pwa/icons }
	Copy-Item (Join-Path $project "web\pwa\manifest.webmanifest") $pwaRelease -Force
	Copy-Item (Join-Path $project "web\pwa\service-worker.js") $pwaRelease -Force
	Copy-Item (Join-Path $project "web\pwa\icons") $pwaRelease -Recurse -Force

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

	if ((Get-Item $exePath).Length -le 0) {
		throw "TaskNotifier.exe is empty."
	}
	if (-not (Test-Path (Join-Path $pwaRelease "index.html"))) {
		throw "PWA index.html is missing."
	}

	(Get-Item $exePath).LastWriteTime = $buildTime
	(Get-Item $buildInfoPath).LastWriteTime = $buildTime

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
	Write-Host "Created $zip ($displayVersion, EXE + PWA, fully offline build)"
}
finally {
	$env:GOPROXY = $oldGoProxy
	$env:GOSUMDB = $oldGoSumDb
	$env:GOFLAGS = $oldGoFlags
}
