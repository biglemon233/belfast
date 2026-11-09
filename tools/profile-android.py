#!/usr/bin/env python3
"""通过 ADB 重放界面操作并保存 CPU、内存和渲染数据，不清除应用数据。

Replays UI actions through ADB and records CPU, memory, and rendering data without clearing data.
"""
import argparse
import csv
import io
import json
from pathlib import Path
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

import importlib.util

_adb_spec = importlib.util.spec_from_file_location("adb_debug", Path(__file__).with_name("adb-debug.py"))
_adb_module = importlib.util.module_from_spec(_adb_spec)
_adb_spec.loader.exec_module(_adb_module)
find_adb = _adb_module.find_adb


class Profiler:
    """保存每个场景的原始证据；操作失败时停止，避免把错误页面算入结果。

    Saves raw evidence per scenario and stops on failed actions to avoid measuring the wrong page.
    """

    def __init__(self, args):
        self.adb = [args.adb or find_adb(), "-s", args.serial]
        self.package = args.package
        self.output = args.output
        self.output.mkdir(parents=True, exist_ok=True)

    def call(self, *args):
        """执行 ADB 并拒绝非零返回码。 / Runs ADB and rejects nonzero exit codes."""
        result = subprocess.run(self.adb + list(args), capture_output=True, timeout=60)
        if result.returncode:
            raise RuntimeError(result.stderr.decode("utf-8", errors="replace"))
        return result.stdout.decode("utf-8", errors="replace")

    def hierarchy(self):
        """读取可访问性树用于定位和验证。 / Reads the accessibility tree for targeting and checks."""
        self.call("shell", "uiautomator", "dump", "/sdcard/azurpilot-profile.xml")
        return self.call("shell", "cat", "/sdcard/azurpilot-profile.xml")

    def action(self, step):
        """执行明确指定的操作；文本目标必须在当前可见树中。 / Runs actions with visible text targets."""
        kind = step[0]
        if kind == "scroll_text":
            for _ in range(10):
                root = ET.fromstring(self.hierarchy())
                if any(node.get("text") == step[1] and node.get("bounds") != "[0,0][0,0]"
                       and node.get("package") == self.package
                       for node in root.iter("node")):
                    self.action(["text", step[1]])
                    return
                self.action(["swipe", *step[2:]])
            raise RuntimeError(f"Text not found after scrolling: {step[1]}")
        elif kind in ("text", "expect", "description"):
            root = ET.fromstring(self.hierarchy())
            attribute = "content-desc" if kind == "description" else "text"
            matches = [node for node in root.iter("node") if node.get(attribute) == step[1]
                       and node.get("package") == self.package
                       and node.get("bounds") != "[0,0][0,0]"]
            if not matches:
                raise RuntimeError(f"Visible {attribute} not found: {step[1]}")
            if kind != "expect":
                bounds = list(map(int, re.findall(r"\d+", matches[0].get("bounds"))))
                self.call("shell", "input", "tap", str((bounds[0] + bounds[2]) // 2),
                          str((bounds[1] + bounds[3]) // 2))
        elif kind == "wait":
            time.sleep(step[1])
        elif kind == "back":
            self.call("shell", "input", "keyevent", "4")
        elif kind in ("tap", "swipe", "keyevent"):
            self.call("shell", "input", kind, *map(str, step[1:]))
        elif kind == "input_text":
            if not re.fullmatch(r"[A-Za-z0-9_.]+", step[1]):
                raise ValueError("input_text accepts only ASCII letters, numbers, underscores, and dots")
            self.call("shell", "input", "text", step[1])
        else:
            raise ValueError(f"Unsupported action: {kind}")

    def cpu_ticks(self, pid):
        """读取应用进程用户态和内核态时钟；非 debug 包可能不允许。

        Reads process user and system ticks; non-debug packages may deny access.
        """
        try:
            raw = self.call("shell", "run-as", self.package, "cat", f"/proc/{pid}/stat")
            fields = raw[raw.rfind(")") + 2:].split()
            return int(fields[11]) + int(fields[12])
        except (RuntimeError, ValueError, IndexError):
            return None

    def measure(self, scenario):
        """重置帧计数后重放场景，并记录应用进程 CPU 与完整内存快照。

        Resets frame counters, replays a scenario, and saves process CPU and full memory snapshots.
        """
        for step in scenario.get("prepare") or []:
            self.action(step)
        name = scenario["name"]
        if not re.fullmatch(r"[a-zA-Z0-9_-]+", name):
            raise ValueError("Scenario names must be safe file names")
        pid = self.call("shell", "pidof", self.package).strip().split()[0]
        self.call("shell", "dumpsys", "gfxinfo", self.package, "reset")
        ticks_before = self.cpu_ticks(pid)
        started = time.monotonic()
        for step in scenario["actions"]:
            self.action(step)
        elapsed = time.monotonic() - started
        if self.call("shell", "pidof", self.package).strip().split()[0] != pid:
            raise RuntimeError("The app process restarted during the scenario; results were discarded")
        ticks_after = self.cpu_ticks(pid)
        gfx = self.call("shell", "dumpsys", "gfxinfo", self.package, "framestats")
        memory = self.call("shell", "dumpsys", "meminfo", self.package)
        (self.output / f"{name}-gfx.txt").write_text(gfx, encoding="utf-8")
        (self.output / f"{name}-memory.txt").write_text(memory, encoding="utf-8")
        hierarchy = self.hierarchy()
        root = ET.fromstring(hierarchy)
        visible = {node.get("text") for node in root.iter("node")
                   if node.get("package") == self.package and node.get("bounds") != "[0,0][0,0]"}
        missing = set(scenario.get("verify", [])) - visible
        if missing:
            raise RuntimeError(f"Wrong destination in {name}: missing visible text {sorted(missing)}")
        (self.output / f"{name}.xml").write_text(hierarchy, encoding="utf-8")
        self.call("shell", "screencap", "-p", "/sdcard/azurpilot-profile.png")
        self.call("pull", "/sdcard/azurpilot-profile.png", str(self.output / f"{name}.png"))
        summary = summarize(gfx, memory)
        summary.update(name=name, elapsed_seconds=round(elapsed, 3), pid=pid)
        if ticks_before is not None and ticks_after is not None and ticks_after >= ticks_before:
            # Android 的 USER_HZ 固定为 100；100% 表示占满一个核心，含 ADB 操作间隔。
            summary["cpu_seconds"] = (ticks_after - ticks_before) / 100
            summary["cpu_one_core_percent"] = round(summary["cpu_seconds"] / elapsed * 100, 2)
        print(json.dumps(summary, ensure_ascii=False), flush=True)
        return summary


def summarize(gfx, memory):
    """解析 HWUI 汇总与逐帧耗时，GPU 时间只在设备提供时报告。

    Parses HWUI summaries and frame durations, reporting GPU time only when the device provides it.
    """
    result = {}
    patterns = {
        "frames": r"Total frames rendered: (\d+)",
        "janky_frames": r"Janky frames: (\d+)",
        "jank_percent": r"Janky frames: \d+ \(([\d.]+)%\)",
        "p50_ms": r"50th percentile: (\d+)ms",
        "p90_ms": r"90th percentile: (\d+)ms",
        "p95_ms": r"95th percentile: (\d+)ms",
        "p99_ms": r"99th percentile: (\d+)ms",
        "gpu_p95_ms": r"95th gpu percentile: (\d+)ms",
        "gpu_memory_bytes": r"Total GPU memory usage:\s*(\d+) bytes",
    }
    for key, pattern in patterns.items():
        match = re.search(pattern, gfx)
        if match:
            result[key] = float(match[1]) if "." in match[1] else int(match[1])
    durations = []
    for block in gfx.split("---PROFILEDATA---")[1::2]:
        for row in csv.DictReader(io.StringIO(block.strip())):
            try:
                if int(row["Flags"]) == 0:
                    duration = (int(row["FrameCompleted"]) - int(row["IntendedVsync"])) / 1e6
                    if 0 < duration < 10000:
                        durations.append(duration)
            except (ValueError, KeyError, TypeError):
                pass
    if durations:
        durations.sort()
        result.update(frame_sample_count=len(durations), max_frame_ms=round(max(durations), 2),
                      sample_p95_ms=round(durations[max(0, (len(durations) * 95 + 99) // 100 - 1)], 2),
                      sample_frames_over_8_33ms=sum(value > 1000 / 120 for value in durations),
                      sample_frames_over_10ms=sum(value > 10 for value in durations),
                      frames_over_50ms=sum(value > 50 for value in durations))
    for key, pattern in {
        "total_pss_kb": r"TOTAL PSS:\s*(\d+)",
        "java_heap_kb": r"Java Heap:\s*(\d+)",
        "native_heap_kb": r"Native Heap:\s*(\d+)",
        "graphics_kb": r"Graphics:\s*(\d+)",
        "views": r"Views:\s*(\d+)",
    }.items():
        match = re.search(pattern, memory)
        if match:
            result[key] = int(match[1])
    return result


def main():
    """执行 JSON 场景列表并保存可比较汇总。 / Executes JSON scenarios and saves comparable summaries."""
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serial", required=True)
    parser.add_argument("--adb")
    parser.add_argument("--package", default="com.azurpilot.ghio")
    parser.add_argument("--scenarios", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    scenarios = json.loads(args.scenarios.read_text(encoding="utf-8-sig"))
    for scenario in scenarios:
        for steps in (scenario.get("prepare") or [], scenario["actions"]):
            if not isinstance(steps, list) or any(not isinstance(step, list) or not step for step in steps):
                raise ValueError("Each action must be a nonempty JSON array inside an actions array")
    profiler = Profiler(args)
    metadata = {"serial": args.serial, "package": args.package,
                "model": profiler.call("shell", "getprop", "ro.product.model").strip(),
                "android": profiler.call("shell", "getprop", "ro.build.version.release").strip(),
                "frame_scope": "HWUI histogram for all frames; PROFILEDATA sample retains a bounded recent-frame window",
                "cpu_scope": "App process; percent of one core; includes ADB action gaps",
                "gpu_scope": "HWUI GPU frame time and cache memory, not device-wide GPU utilization"}
    summaries = []
    try:
        for scenario in scenarios:
            summaries.append(profiler.measure(scenario))
    finally:
        (args.output / "summary.json").write_text(
            json.dumps({"metadata": metadata, "scenarios": summaries}, ensure_ascii=False, indent=2),
            encoding="utf-8")


if __name__ == "__main__":
    main()
