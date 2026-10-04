# Adds or removes the directory in $env:S3ANYSYNC_DIR from the per-user PATH.
# Run by the installer and uninstaller; it does not broadcast the change (the caller does).
param([Parameter(Mandatory)][ValidateSet('Add', 'Remove')][string]$Action)
$ErrorActionPreference = 'Stop'
$dir = $env:S3ANYSYNC_DIR
if ([string]::IsNullOrEmpty($dir)) { exit 2 }

$k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
try {
  # Raw read: %VARS% in the existing value stay unexpanded.
  $raw = [string]$k.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
  $parts = @($raw.Split(';') | Where-Object { $_ -ne '' })
  $present = @($parts | Where-Object { $_ -ieq $dir }).Count -gt 0
  $new = $null
  if ($Action -eq 'Add') {
    if (-not $present) { $new = @($parts + $dir) -join ';' }
  } else {
    if ($present) { $new = @($parts | Where-Object { $_ -ine $dir }) -join ';' }
  }
  # Write only when the list changed. The value is always stored as REG_EXPAND_SZ,
  # even when it was REG_SZ before: acceptable, %VARS% then keep working.
  if ($null -ne $new) {
    $k.SetValue('Path', $new, [Microsoft.Win32.RegistryValueKind]::ExpandString)
  }
} finally {
  $k.Close()
}
exit 0
