#!/usr/bin/env python3
"""AzurPilot 模板匹配自动化测试与诊断工具。

本工具用于自主驱动 AzurPilot 页面导航，逐个进入各个游戏页面并进行高精度的
模板匹配自动化体检，统计得分分布、临界悬空区（0.80~0.85 概率性失败根因）、
通道反转异常、空间位置漂移，并自动导出坏帧比对热力图。

Automated template matching benchmark and diagnostic suite for AzurPilot.
Autonomously navigates through game pages, performs multi-frame sampling,
evaluates similarity distributions and borderline risks (0.80~0.85),
diagnoses channel/drift anomalies, and dumps visual comparison heatmaps.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import sys
import time
from typing import Any, Dict, List, Optional, Tuple

import cv2
import numpy as np


def find_alas_dir() -> Path:
    """定位本地 AzurLaneAutoScript 源码根目录。"""
    candidates = [
        Path(r"C:\Users\AzurLane\Desktop\Projects\AzurLaneAutoScript"),
        Path.cwd() / "AzurLaneAutoScript",
        Path.cwd().parent / "AzurLaneAutoScript",
        Path.cwd(),
    ]
    for c in candidates:
        if (c / "module" / "ui" / "ui.py").is_file():
            return c.resolve()
    raise RuntimeError("无法定位 AzurLaneAutoScript 项目根目录，请通过环境或工作区检查。")


ALAS_DIR = find_alas_dir()
if str(ALAS_DIR) not in sys.path:
    sys.path.insert(0, str(ALAS_DIR))

# 初始化区服
import module.config.server as server  # noqa: E402
from module.base.button import Button  # noqa: E402
from module.base.utils import crop, rgb2luma  # noqa: E402
from module.ui import assets as ui_assets  # noqa: E402
from module.ui.page import (  # noqa: E402
    Page,
    page_academy,
    page_campaign,
    page_campaign_menu,
    page_commission,
    page_daily,
    page_dock,
    page_dorm,
    page_dormmenu,
    page_exercise,
    page_fleet,
    page_main,
    page_main_white,
    page_mission,
    page_research,
    page_reshmenu,
    page_reward,
    page_shipyard,
    page_storage,
    page_tactical,
)


def get_page_buttons(page: Page) -> List[Tuple[str, Button]]:
    """自动获取属于该页面的所有关联按钮（check_button、导航出口按钮等）。"""
    buttons: Dict[str, Button] = {}
    if page.check_button and getattr(page.check_button, "file", None):
        name = getattr(page.check_button, "name", f"{page.name}_CHECK")
        buttons[name] = page.check_button

    for dest, btn in page.links.items():
        if btn and getattr(btn, "file", None):
            name = getattr(btn, "name", f"LINK_{getattr(dest, 'name', 'DEST')}")
            buttons[name] = btn

    # 常见页面附加特定按钮检测
    if page == page_campaign:
        for extra in [ui_assets.BACK_ARROW, ui_assets.GOTO_MAIN]:
            if getattr(extra, "file", None):
                buttons[getattr(extra, "name", "EXTRA")] = extra
    elif page == page_academy:
        for extra in [ui_assets.ACADEMY_GOTO_MUNITIONS, ui_assets.ACADEMY_GOTO_GAME_ROOM]:
            if getattr(extra, "file", None):
                buttons[getattr(extra, "name", "EXTRA")] = extra

    return list(buttons.items())

# 默认自主巡航路线：覆盖最常用核心模块，最后安全回退到主界面
DEFAULT_TOUR_ROUTE: List[Page] = [
    page_main,
    page_campaign_menu,
    page_campaign,
    page_daily,
    page_exercise,
    page_reward,
    page_commission,
    page_tactical,
    page_dock,
    page_storage,
    page_reshmenu,
    page_research,
    page_shipyard,
    page_dormmenu,
    page_academy,
    page_dorm,
    page_mission,
    page_fleet,
    page_main,  # 巡航结束归位
]


class TemplateMatchingAuditor:
    """自动化模板匹配巡检与诊断运行器。"""

    def __init__(
        self,
        config_name: str = "ap",
        server_name: str = "cn",
        sample_frames: int = 10,
        sample_interval: float = 0.15,
        threshold: float = 0.85,
        borderline_low: float = 0.80,
        dump_dir: Path = Path("output/template_audit"),
        offline_image: Optional[Path] = None,
    ):
        self.config_name = config_name
        self.server_name = server_name
        self.sample_frames = sample_frames
        self.sample_interval = sample_interval
        self.threshold = threshold
        self.borderline_low = borderline_low
        self.dump_dir = dump_dir.resolve()
        self.dump_dir.mkdir(parents=True, exist_ok=True)
        self.offline_image = offline_image

        server.server = server_name
        self.alas: Optional[Any] = None
        self.results: List[Dict[str, Any]] = []

    def init_alas(self):
        """初始化 AzurPilot UI 驱动实例，配置为直连虚拟屏桥。"""
        if self.offline_image is not None:
            return

        from module.config.config import AzurLaneConfig
        from module.ui.ui import UI

        print(f"[*] 正在初始化 AzurPilot 运行时 (config='{self.config_name}', server='{self.server_name}')...")
        cfg = AzurLaneConfig(self.config_name)
        cfg.override(
            Emulator_ScreenshotMethod="azurpilot_android",
            Emulator_ControlMethod="azurpilot_android",
            Emulator_Serial="azurpilot_android",
        )

        self.alas = UI(cfg)
        print("[*] 正在连接虚拟屏与桥通信端口 (127.0.0.1:22301)...")
        # 执行一次预截图验证联通性
        self.alas.device.screenshot()
        h, w = self.alas.device.image.shape[:2]
        print(f"[+] 虚拟屏连接成功！当前捕获分辨率: {w}x{h}")
        if w != 1280 or h != 720:
            print(f"[!] 警告: 当前分辨率为 {w}x{h}，非标准 1280x720！")

    def capture_frame(self) -> np.ndarray:
        """捕获一帧 RGB 图像。"""
        if self.offline_image is not None:
            img = cv2.imread(str(self.offline_image))
            if img is None:
                raise FileNotFoundError(f"无法读取离线图像: {self.offline_image}")
            return cv2.cvtColor(img, cv2.COLOR_BGR2RGB)

        assert self.alas is not None
        self.alas.device.screenshot()
        return self.alas.device.image

    def evaluate_button_on_image(
        self,
        button: Button,
        img_rgb: np.ndarray,
        offset_xy: Tuple[int, int] = (3, 30),
    ) -> Dict[str, Any]:
        """在单帧图像上对目标 Button 执行多维度算法匹配与消融分析。"""
        button.ensure_template()
        if button.image is None or button.is_gif:
            return {"error": "模板图像不存在或为 GIF"}

        templ_rgb = button.image
        h_t, w_t = templ_rgb.shape[:2]

        # 1. 标准 RGB 模板匹配 (严格 offset 区域)
        offset = np.array((-offset_xy[0], -offset_xy[1], offset_xy[0], offset_xy[1]))
        search_rect = offset + button.area
        crop_rgb = crop(img_rgb, search_rect, copy=False)

        res_rgb = cv2.matchTemplate(crop_rgb, templ_rgb, cv2.TM_CCOEFF_NORMED)
        _, max_val_rgb, _, max_loc_rgb = cv2.minMaxLoc(res_rgb)
        drift_x = max_loc_rgb[0] - offset_xy[0]
        drift_y = max_loc_rgb[1] - offset_xy[1]

        # 2. 通道反转消融 (BGR 匹配)
        img_bgr = img_rgb[..., 2::-1]
        crop_bgr = crop(img_bgr, search_rect, copy=False)
        res_bgr = cv2.matchTemplate(crop_bgr, templ_rgb, cv2.TM_CCOEFF_NORMED)
        _, max_val_bgr, _, _ = cv2.minMaxLoc(res_bgr)

        # 3. 亮度 / 灰度匹配 (Luma)
        crop_luma = rgb2luma(crop_rgb)
        templ_luma = rgb2luma(templ_rgb)
        res_luma = cv2.matchTemplate(crop_luma, templ_luma, cv2.TM_CCOEFF_NORMED)
        _, max_val_luma, _, _ = cv2.minMaxLoc(res_luma)

        # 4. 二值化匹配 (Binary OTSU)
        _, crop_bin = cv2.threshold(crop_luma, 0, 255, cv2.THRESH_BINARY | cv2.THRESH_OTSU)
        _, templ_bin = cv2.threshold(templ_luma, 0, 255, cv2.THRESH_BINARY | cv2.THRESH_OTSU)
        res_bin = cv2.matchTemplate(crop_bin, templ_bin, cv2.TM_CCOEFF_NORMED)
        _, max_val_bin, _, _ = cv2.minMaxLoc(res_bin)

        # 5. 颜色检测 (Button.color 距离)
        color_ok = button.appear_on(img_rgb, threshold=10)

        # 6. 大范围空间寻优 (若局部匹配低于阈值，扩大到 60x60 检测是否严重漂移)
        wide_sim = max_val_rgb
        wide_drift = (drift_x, drift_y)
        if max_val_rgb < self.threshold:
            wide_offset = np.array((-60, -60, 60, 60))
            wide_crop = crop(img_rgb, wide_offset + button.area, copy=False)
            if wide_crop.shape[0] >= h_t and wide_crop.shape[1] >= w_t:
                res_wide = cv2.matchTemplate(wide_crop, templ_rgb, cv2.TM_CCOEFF_NORMED)
                _, wide_val, _, wide_loc = cv2.minMaxLoc(res_wide)
                wide_sim = wide_val
                wide_drift = (wide_loc[0] - 60, wide_loc[1] - 60)

        return {
            "sim_rgb": float(max_val_rgb),
            "sim_bgr": float(max_val_bgr),
            "sim_luma": float(max_val_luma),
            "sim_bin": float(max_val_bin),
            "drift_xy": (int(drift_x), int(drift_y)),
            "wide_sim": float(wide_sim),
            "wide_drift": (int(wide_drift[0]), int(wide_drift[1])),
            "color_ok": bool(color_ok),
            "cropped_rgb": crop(img_rgb, button.area, copy=True),
            "template_rgb": templ_rgb,
        }

    def audit_buttons_multiframe(
        self,
        page_name: str,
        buttons: List[Tuple[str, Button]],
    ) -> List[Dict[str, Any]]:
        """对指定页面上的按钮集合进行多帧时序采样和统计。"""
        print(f"\n[{page_name}] 正在采样 {self.sample_frames} 帧以检测动画与相似度抖动...")
        frames: List[np.ndarray] = []
        for i in range(self.sample_frames):
            frames.append(self.capture_frame())
            if i < self.sample_frames - 1 and self.sample_interval > 0:
                time.sleep(self.sample_interval)

        page_results = []
        for btn_name, btn in buttons:
            scores_rgb = []
            eval_list = []
            for f in frames:
                eval_res = self.evaluate_button_on_image(btn, f)
                if "error" in eval_res:
                    continue
                scores_rgb.append(eval_res["sim_rgb"])
                eval_list.append(eval_res)

            if not scores_rgb:
                continue

            max_score = float(np.max(scores_rgb))
            min_score = float(np.min(scores_rgb))
            mean_score = float(np.mean(scores_rgb))
            std_score = float(np.std(scores_rgb))
            pass_count = sum(1 for s in scores_rgb if s >= self.threshold)
            pass_rate = pass_count / len(scores_rgb)

            # 最新一帧的深度数据
            latest_eval = eval_list[-1]

            # 状态判定
            # 🟢 PASS: 稳定达标
            # 🟡 BORDERLINE: 相似度在 0.80~0.85 之间或极差导致部分掉线
            # 🔴 FAIL: 相似度 < 0.80
            # ⚪ ABSENT: 极低（< 0.50，通常代表元素当前不在页面上）
            if mean_score >= self.threshold and min_score >= self.threshold:
                status = "PASS"
            elif mean_score >= self.borderline_low or (max_score >= self.threshold and min_score < self.threshold):
                status = "BORDERLINE"
            elif mean_score >= 0.55:
                status = "FAIL"
            else:
                status = "ABSENT"

            # 诊断告警标签
            diagnostics = []
            if latest_eval["sim_bgr"] > latest_eval["sim_rgb"] + 0.15:
                diagnostics.append("红蓝通道疑似反转(BGR>RGB)")
            if latest_eval["wide_sim"] >= self.threshold and latest_eval["sim_rgb"] < self.threshold:
                dx, dy = latest_eval["wide_drift"]
                diagnostics.append(f"坐标漂移(偏距x:{dx},y:{dy})")
            if std_score > 0.03:
                diagnostics.append(f"帧间抖动剧烈(Std:{std_score:.3f})")
            if latest_eval["sim_luma"] >= self.threshold and latest_eval["sim_rgb"] < self.threshold:
                diagnostics.append("仅色彩微变(Luma达标)")

            record = {
                "page": page_name,
                "button": btn_name,
                "status": status,
                "max": max_score,
                "min": min_score,
                "mean": mean_score,
                "std": std_score,
                "pass_rate": pass_rate,
                "sim_bgr": latest_eval["sim_bgr"],
                "sim_luma": latest_eval["sim_luma"],
                "sim_bin": latest_eval["sim_bin"],
                "drift_xy": latest_eval["drift_xy"],
                "color_ok": latest_eval["color_ok"],
                "diagnostics": diagnostics,
            }
            page_results.append(record)

            # 对 BORDERLINE 或 FAIL 保存比对切片
            if status in ("BORDERLINE", "FAIL"):
                self.dump_diff_image(page_name, btn_name, latest_eval)

        return page_results

    def dump_diff_image(self, page_name: str, btn_name: str, eval_data: Dict[str, Any]):
        """生成并落盘真实裁剪区、模板图与绝对差值热力图的对照卡片。"""
        crop_rgb = eval_data["cropped_rgb"]
        templ_rgb = eval_data["template_rgb"]

        # 尺寸统一到模板大小
        h, w = templ_rgb.shape[:2]
        if crop_rgb.shape[:2] != (h, w):
            crop_rgb = cv2.resize(crop_rgb, (w, h))

        # 计算像素绝对差值并着色
        diff = cv2.absdiff(crop_rgb, templ_rgb)
        diff_gray = cv2.cvtColor(diff, cv2.COLOR_RGB2GRAY)
        heatmap = cv2.applyColorMap(diff_gray, cv2.COLORMAP_JET)
        heatmap_rgb = cv2.cvtColor(heatmap, cv2.COLOR_BGR2RGB)

        # 拼合成并排比对图 [真实截屏 | 原版模板 | 差值热力图]
        combined = np.hstack([crop_rgb, templ_rgb, heatmap_rgb])
        out_page_dir = self.dump_dir / page_name
        out_page_dir.mkdir(parents=True, exist_ok=True)
        out_path = out_page_dir / f"{btn_name}_diff.png"
        cv2.imwrite(str(out_path), cv2.cvtColor(combined, cv2.COLOR_RGB2BGR))

    def run_autonomous_tour(self, pages: Optional[List[Page]] = None):
        """执行自主多页面巡航并全流程体检。"""
        tour_list = pages or DEFAULT_TOUR_ROUTE
        print("=" * 78)
        print("                 AzurPilot 自动化多页面模板匹配巡检                 ")
        print("=" * 78)
        print(f"[*] 巡航规划: 共 {len(tour_list)} 个节点")

        # 确保初始状态回到主界面
        print("[*] 正在对齐游戏初始状态 (ui_ensure page_main)...")
        assert self.alas is not None
        self.alas.ui_ensure(page_main)
        time.sleep(1.0)

        for idx, target_page in enumerate(tour_list):
            page_name = getattr(target_page, "name", str(target_page))
            print(f"\n>>> [{idx + 1}/{len(tour_list)}] 正在导航至: {page_name}")
            try:
                self.alas.ui_ensure(target_page)
                # 留出 0.5 秒让页面过渡动效稳定
                time.sleep(0.5)
            except Exception as e:
                print(f"[!] 导航至 {page_name} 失败: {e}")
                continue

            buttons = get_page_buttons(target_page)

            if buttons:
                page_audit = self.audit_buttons_multiframe(page_name, buttons)
                self.results.extend(page_audit)
                self.print_page_summary(page_name, page_audit)

        self.generate_final_report()

    def run_current_screen_audit(self):
        """仅对当前屏幕或离线截图进行全量 UI 按钮扫描。"""
        print("=" * 78)
        print("                 当前屏幕全量 UI 模板匹配扫描                       ")
        print("=" * 78)
        img_rgb = self.capture_frame()

        all_buttons: List[Tuple[str, Button]] = []
        for name in dir(ui_assets):
            btn = getattr(ui_assets, name)
            if isinstance(btn, Button) and btn.file:
                all_buttons.append((name, btn))

        print(f"[*] 正在匹配当前画面的 {len(all_buttons)} 个已知 UI 模板...")
        audit_results = []
        for btn_name, btn in all_buttons:
            eval_res = self.evaluate_button_on_image(btn, img_rgb)
            if "error" in eval_res:
                continue

            score = eval_res["sim_rgb"]
            # 仅报告相关度 > 0.50 的元素或处于临界态的元素
            if score >= 0.55:
                if score >= self.threshold:
                    status = "PASS"
                elif score >= self.borderline_low:
                    status = "BORDERLINE"
                else:
                    status = "FAIL"

                diagnostics = []
                if eval_res["sim_bgr"] > score + 0.15:
                    diagnostics.append("红蓝通道疑似反转(BGR>RGB)")
                if eval_res["wide_sim"] >= self.threshold and score < self.threshold:
                    dx, dy = eval_res["wide_drift"]
                    diagnostics.append(f"坐标漂移(偏距x:{dx},y:{dy})")

                rec = {
                    "page": "CURRENT_SCREEN",
                    "button": btn_name,
                    "status": status,
                    "max": score,
                    "min": score,
                    "mean": score,
                    "std": 0.0,
                    "pass_rate": 1.0 if score >= self.threshold else 0.0,
                    "sim_bgr": eval_res["sim_bgr"],
                    "sim_luma": eval_res["sim_luma"],
                    "sim_bin": eval_res["sim_bin"],
                    "drift_xy": eval_res["drift_xy"],
                    "color_ok": eval_res["color_ok"],
                    "diagnostics": diagnostics,
                }
                audit_results.append(rec)
                if status in ("BORDERLINE", "FAIL"):
                    self.dump_diff_image("CURRENT_SCREEN", btn_name, eval_res)

        self.results = audit_results
        self.print_page_summary("CURRENT_SCREEN", audit_results)
        self.generate_final_report()

    def print_page_summary(self, page_name: str, records: List[Dict[str, Any]]):
        """终端打印单页简要表格。"""
        if not records:
            print(f"[{page_name}] 无需审计的按钮")
            return

        print(f"\n--- {page_name} 体检汇总 ---")
        fmt = "{:<28} {:<10} {:<8} {:<8} {:<8} {:<8} {:<24}"
        print(fmt.format("Button", "Status", "Mean", "Min", "Max", "PassRate", "Diagnostics"))
        print("-" * 100)
        for r in records:
            if r["status"] == "ABSENT":
                continue
            status_tag = {
                "PASS": "\033[92mPASS\033[0m",
                "BORDERLINE": "\033[93mBORDERLINE\033[0m",
                "FAIL": "\033[91mFAIL\033[0m",
            }.get(r["status"], r["status"])

            diag_str = ", ".join(r["diagnostics"]) if r["diagnostics"] else "-"
            print(
                fmt.format(
                    r["button"][:28],
                    status_tag,
                    f"{r['mean']:.4f}",
                    f"{r['min']:.4f}",
                    f"{r['max']:.4f}",
                    f"{r['pass_rate'] * 100:.0f}%",
                    diag_str,
                )
            )

    def generate_final_report(self):
        """生成并落盘最终体检报告，标记概率性失败风险。"""
        report_file = self.dump_dir / "report.json"
        with open(report_file, "w", encoding="utf-8") as f:
            json.dump(self.results, f, ensure_ascii=False, indent=2)

        borderlines = [r for r in self.results if r["status"] == "BORDERLINE"]
        fails = [r for r in self.results if r["status"] == "FAIL"]
        passes = [r for r in self.results if r["status"] == "PASS"]

        print("\n" + "=" * 78)
        print("                         总体诊断报告汇总                         ")
        print("=" * 78)
        print(f"[*] 共检测按钮项: {len(self.results)} 项")
        print(f"    🟢 达标健康项 (PASS):        {len(passes)} 项")
        print(f"    🟡 危险临界项 (BORDERLINE):  {len(borderlines)} 项  <-- 概率性失败首要嫌疑！")
        print(f"    🔴 异常失败项 (FAIL):        {len(fails)} 项")
        print(f"[*] 详细 JSON 报告已保存至: {report_file}")
        print(f"[*] 坏帧与热力图已保存至: {self.dump_dir}/")

        if borderlines:
            print("\n[!] 重点关注：以下按钮处于 0.80~0.85 临界悬空区，极易因动画或光照偶发失灵：")
            for b in borderlines:
                diag = f" ({', '.join(b['diagnostics'])})" if b["diagnostics"] else ""
                print(f"    - [{b['page']}] {b['button']}: Mean={b['mean']:.4f}, Min={b['min']:.4f}{diag}")
            print("\n    建议对策:")
            print("    1. 若无坐标漂移且 Luma/BGR 正常，可在 module/base/button.py 中对该按钮适度放宽 threshold (如 0.80)。")
            print("    2. 若存在坐标漂移，请在对应 assets.py 中修正 area 矩形坐标。")
            print("    3. 检查坏帧热力图以确认是否有微小 UI 改动导致像素差异。")
        print("=" * 78 + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", default="ap", help="AP 配置文件名 (默认: ap)")
    parser.add_argument("--server", default="cn", choices=["cn", "en", "jp", "tw"], help="游戏区服 (默认: cn)")
    parser.add_argument("--frames", type=int, default=10, help="每个页面采样帧数 (默认: 10)")
    parser.add_argument("--interval", type=float, default=0.15, help="采样间隔秒数 (默认: 0.15s)")
    parser.add_argument("--threshold", type=float, default=0.85, help="相似度通过阈值 (默认: 0.85)")
    parser.add_argument("--borderline-low", type=float, default=0.80, help="临界区下界 (默认: 0.80)")
    parser.add_argument("--current-only", action="store_true", help="不进行页面跳转，仅体检当前画面")
    parser.add_argument("--offline", type=Path, help="离线图像路径 (跳过设备连接)")
    parser.add_argument("--dump-dir", type=Path, default=Path("output/template_audit"), help="差值热力图输出目录")
    parser.add_argument("--pages", nargs="+", help="指定巡检的页面变量名 (例如: page_main page_campaign_menu)")

    args = parser.parse_args()

    auditor = TemplateMatchingAuditor(
        config_name=args.config,
        server_name=args.server,
        sample_frames=args.frames,
        sample_interval=args.interval,
        threshold=args.threshold,
        borderline_low=args.borderline_low,
        dump_dir=args.dump_dir,
        offline_image=args.offline,
    )

    if args.offline or args.current_only:
        if not args.offline:
            auditor.init_alas()
        auditor.run_current_screen_audit()
    else:
        auditor.init_alas()
        target_pages = None
        if args.pages:
            target_pages = []
            for p_name in args.pages:
                if p_name in Page.all_pages:
                    target_pages.append(Page.all_pages[p_name])
                else:
                    print(f"[!] 警告: 未知页面 '{p_name}'，已忽略。可选页面: {list(Page.all_pages.keys())}")
        auditor.run_autonomous_tour(target_pages)


if __name__ == "__main__":
    main()
