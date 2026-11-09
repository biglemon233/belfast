<#
.SYNOPSIS
连接 Android 设备并持续输出应用或 PRoot 会话日志。

Connects to an Android device and continuously prints app or PRoot session logs.

.DESCRIPTION
`app` 模式按目标应用 pid 过滤 logcat；`session` 模式跟随 guest 内 PRoot 会话日志。
默认 adb 路径适配 MuMu Player，其他设备应传入 `-Adb` 和 `-Serial`。此脚本只读取设备
日志，不安装 APK、不修改设备状态。

The `app` mode filters logcat by the target-app pid; `session` tails the guest PRoot session log.
The default adb path targets MuMu Player; pass `-Adb` and `-Serial` for another device. This script
only reads device logs: it neither installs APKs nor changes device state.

.PARAMETER Source
`app` 读取 logcat，`session` 跟随 PRoot 会话文件。
`app` reads logcat; `session` follows the PRoot session file.

.PARAMETER Serial
adb 设备序列号；默认值是本地 MuMu Player 地址。
The adb device serial; the default is the local MuMu Player address.

.PARAMETER Adb
adb 可执行文件路径。
The path to the adb executable.

.NOTES
应用包名必须与当前构建的 applicationId 一致。按 Ctrl+C 停止；adb 连接或目标进程不可用
时脚本以错误终止，避免显示来自错误设备的日志。
The application package must match the current build's applicationId. Press Ctrl+C to stop. adb
connection or target-process failures terminate with an error so logs from the wrong device are not
shown.
#>
param(
    [ValidateSet("app", "session")]
    [string]$Source = "app",
    [string]$Serial = "127.0.0.1:16384",
    [string]$Adb = "D:\MuMuPlayer\nx_device\15.0\shell\adb.exe"
)

$ErrorActionPreference = "Stop"

# 必须跟 app/build-logic 的 applicationId 同步；pid 过滤与外部存储日志路径均依赖它。
# This must stay synchronized with applicationId in app/build-logic; both pid filtering and external
# storage log paths depend on it.
$package = "com.azurpilot.ghio"

if (-not (Test-Path -LiteralPath $Adb -PathType Leaf)) {
    throw "adb not found: $Adb"
}

& $Adb connect $Serial | Out-Host
if ($LASTEXITCODE -ne 0) { throw "adb connect failed: $Serial" }

if ($Source -eq "session") {
    $path = "/sdcard/Android/data/$package/files/log/proot/session.log"
    Write-Host "Watching $path (Ctrl+C to stop)"
    & $Adb -s $Serial shell "tail -n 100 -F $path"
    exit $LASTEXITCODE
}

$pidText = (& $Adb -s $Serial shell "pidof $package").Trim()
if (-not $pidText) { throw "$package is not running" }
$pidValue = ($pidText -split "\s+")[0]
Write-Host "Watching logcat for $package pid=$pidValue (Ctrl+C to stop)"
& $Adb -s $Serial logcat -v time --pid=$pidValue
exit $LASTEXITCODE
