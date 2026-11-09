"""验证 ADB 指标解析，防止无效帧或缺失 GPU 数据导致错误的性能结论。

Checks ADB metric parsing so invalid frames and missing GPU data cannot skew performance claims.
"""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("profile_android", Path(__file__).parents[1] / "profile-android.py")
profiler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profiler)


class FrameMetricsTest(unittest.TestCase):
    """覆盖多窗口帧、无效记录与设备缺省字段。 / Covers multiple windows, invalid rows, and absent fields."""

    def test_histogram_and_recent_frame_window_remain_separate(self):
        """累计分位数不应被有限逐帧窗口替换。 / Keeps cumulative percentiles separate from recent frames."""
        gfx = """Total frames rendered: 300
Janky frames: 30 (10.00%)
95th percentile: 9ms
---PROFILEDATA---
Flags,IntendedVsync,FrameCompleted,
0,1000000,9000000,
0,2000000,13000000,
1,3000000,999999999,
0,4000000,0,
---PROFILEDATA---
---PROFILEDATA---
Flags,IntendedVsync,FrameCompleted,
0,5000000,15000000,
0,0,9223372036854775807,
---PROFILEDATA---
"""
        result = profiler.summarize(gfx, "TOTAL PSS: 120000\nGraphics: 40000")
        self.assertEqual(result["frames"], 300)
        self.assertEqual(result["p95_ms"], 9)
        self.assertEqual(result["frame_sample_count"], 3)
        self.assertEqual(result["max_frame_ms"], 11)
        self.assertEqual(result["sample_frames_over_8_33ms"], 2)
        self.assertEqual(result["sample_frames_over_10ms"], 1)
        self.assertEqual(result["total_pss_kb"], 120000)
        self.assertNotIn("gpu_p95_ms", result)

    def test_missing_metrics_are_not_reported_as_zero(self):
        """设备不提供数据时保持缺失，不能伪称零占用。 / Omits unavailable metrics instead of claiming zero usage."""
        self.assertEqual(profiler.summarize("", ""), {})

    def test_gpu_time_is_not_gpu_utilization(self):
        """分别保留 GPU 帧耗时与缓存字节数。 / Separately preserves GPU frame time and cache bytes."""
        result = profiler.summarize("95th gpu percentile: 5ms\nTotal GPU memory usage: 123456 bytes", "")
        self.assertEqual(result, {"gpu_p95_ms": 5, "gpu_memory_bytes": 123456})


if __name__ == "__main__":
    unittest.main()
