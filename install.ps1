# SPDX-License-Identifier: AGPL-3.0-only
param([Parameter(Mandatory=$true)][string]$Tablet)
$ErrorActionPreference = 'Stop'
$build = Join-Path $PSScriptRoot 'build'
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$helper = Join-Path $build "drive-cloud-setup-windows-$arch.exe"
$bundle = Join-Path $build 'remarkable-drive-cloud-linux-armv7.tar.gz'
$manifest = Join-Path $build 'SHA256SUMS'
if (!(Test-Path -LiteralPath $manifest)) { throw 'Build development artifacts first: python scripts/build_development.py' }
$sums = @{}
foreach ($line in Get-Content -LiteralPath $manifest) {
    if ($line -match '^([a-f0-9]{64})  ([A-Za-z0-9._-]+)$') { $sums[$Matches[2]] = $Matches[1] }
}
foreach ($file in @($helper, $bundle)) {
    $name = Split-Path -Leaf $file
    if (!$sums.ContainsKey($name) -or (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant() -ne $sums[$name]) { throw "Checksum mismatch: $name" }
}
& $helper install --host $Tablet --bundle $bundle --sha256 $sums[(Split-Path -Leaf $bundle)]
if ($LASTEXITCODE -ne 0) { throw "Installation failed: $LASTEXITCODE" }
