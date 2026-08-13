$ErrorActionPreference = "Stop"

$repo = if ($env:NOSLEEP_REPO) { $env:NOSLEEP_REPO } else { "yansslo/nosleep" }
$installDir = if ($env:NOSLEEP_INSTALL_DIR) { $env:NOSLEEP_INSTALL_DIR } else { Join-Path $HOME ".local\bin" }
$binaryName = if ($env:NOSLEEP_BINARY_NAME) { $env:NOSLEEP_BINARY_NAME } else { "nosleep.exe" }

$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
$arch = switch ($architecture) {
    "x64" { "x64" }
    "arm64" { "arm64" }
    default { throw "Unsupported architecture: $architecture" }
}

$assetName = "nosleep-windows-$arch.exe"
if ($env:NOSLEEP_BINARY_URL) {
    $binaryUrl = $env:NOSLEEP_BINARY_URL
} elseif ($env:NOSLEEP_VERSION) {
    $binaryUrl = "https://github.com/$repo/releases/download/$($env:NOSLEEP_VERSION)/$assetName"
} else {
    $binaryUrl = "https://github.com/$repo/releases/latest/download/$assetName"
}

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$destination = Join-Path $installDir $binaryName
$temporaryFile = Join-Path ([System.IO.Path]::GetTempPath()) ("nosleep-" + [guid]::NewGuid() + ".exe")

try {
    Write-Host "Downloading $assetName from $binaryUrl"
    Invoke-WebRequest -Uri $binaryUrl -OutFile $temporaryFile
    Move-Item -Force -Path $temporaryFile -Destination $destination
} finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $temporaryFile
}

Write-Host "Installed $binaryName to $destination"
$pathEntries = $env:PATH -split ";"
if ($installDir -notin $pathEntries) {
    Write-Host ""
    Write-Host "Add $installDir to your PATH to run nosleep from any terminal."
}
