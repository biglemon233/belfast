# 模板匹配自动化测试与诊断 / Automated template matching benchmark and diagnostics

> 本文说明 AzurPilot 模板匹配自动化测试工具的架构、诊断指标及使用方法。
> This document describes the architecture, diagnostic metrics, and usage of the automated template matching benchmark tool for AzurPilot.

## 中文

### 背景与定位

在基于 Android 虚拟显示器（VirtualDisplay 1280×720）的自动化运行环境中，部分用户可能会遇到“模板匹配概率性失败”的问题。此类偶发故障通常并非代码崩溃，而是由以下因素引起的临界抖动：

1. **临界相似度悬空（Borderline Scores）**：原生 720P 分辨率下 AzurPilot 采用严格的 `0.85` 阈值。当页面存在微弱粒子特效、呼吸灯、动态背景光照或过场 Alpha 渐变时，匹配得分在 `0.81~0.84` 之间浮动，导致偶发失灵。
2. **微小空间漂移（Spatial Drift）**：部分按钮在换装、活动更新或新版客户端排版调整后，坐标发生几个像素的偏移，超出默认严格 `offset` 范围。
3. **颜色空间与通道异常（Color / Channel Anomalies）**：RGB 与 BGR 顺序颠倒、或 GPU 色彩渲染微差导致的跨通道欧氏距离漂移。

为精准排查此类问题，开发了自动化诊断工具 `tools/test_template_matching.py`。该工具可全自主驱动游戏遍历各主要页面，进行多帧时序采样与多算法消融对比，并自动导出差值热力图。

---

### 工具架构与工作流

```text
[启动工具]
    │
    ├─► 离线模式 (--offline <path>): 对本地已有历史截图进行单帧/全量扫描
    │
    └─► 联机模式: 通过 ADB 端口转发 (127.0.0.1:22301) 直连虚拟屏特权桥
            │
            ├─► 当前屏幕模式 (--current-only): 对当前静态画面做 130+ 模板扫描
            │
            └─► 自主多页面巡航 (默认):
                    │
                    ▼
            [初始对齐: ui_ensure(page_main)]
                    │
                    ▼
            [沿拓扑图自主跳转各个页面]
            page_main ➔ page_campaign_menu ➔ page_campaign ➔ page_daily
            ➔ page_exercise ➔ page_reward ➔ page_commission ➔ page_tactical
            ➔ page_dock ➔ page_storage ➔ page_reshmenu ➔ page_research
            ➔ page_shipyard ➔ page_dormmenu ➔ page_academy ➔ page_dorm
            ➔ page_mission ➔ page_fleet ➔ 回到 page_main
                    │
                    ▼
            [页面级多维体检]
            - 等待 0.5s 动画过渡稳定
            - 连续捕获 10 帧 (时序连续采样，间隔 150ms)
            - 针对当前页面 check_button 及出口按钮进行比对
            - 统计 Max / Min / Mean / Std / PassRate
            - 深度消融分析 (RGB vs BGR, Luma 亮度, Binary 二值化, 广域漂移寻优)
            - 自动生成 [真实原图 | 原版模板 | 差值热力图] 拼图落盘
                    │
                    ▼
            [生成终端高亮表格与 JSON 报告 (output/template_audit/report.json)]
```

---

### 使用方法

测试工具推荐使用 `AzurLaneAutoScript` 的 Python 虚拟环境执行：

```bash
# 1. 确保 ADB 端口转发建立
adb forward tcp:22301 tcp:22301
adb forward tcp:25548 tcp:25548

# 2. 模式 A: 默认全量自主多页面巡检 (需游戏已启动在虚拟屏)
C:/Users/AzurLane/Desktop/Projects/AzurLaneAutoScript/.venv/Scripts/python.exe tools/test_template_matching.py

# 3. 模式 B: 仅巡检指定关键页面
C:/Users/AzurLane/Desktop/Projects/AzurLaneAutoScript/.venv/Scripts/python.exe tools/test_template_matching.py --pages page_main page_reward page_commission

# 4. 模式 C: 仅体检当前停留在屏幕上的画面 (不跳转页面)
C:/Users/AzurLane/Desktop/Projects/AzurLaneAutoScript/.venv/Scripts/python.exe tools/test_template_matching.py --current-only

# 5. 模式 D: 离线排查历史错误现场截图
C:/Users/AzurLane/Desktop/Projects/AzurLaneAutoScript/.venv/Scripts/python.exe tools/test_template_matching.py --offline <截图路径.png>
```

---

### 参数说明

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--config` | `ap` | 指定使用的 AP 配置文件名 |
| `--server` | `cn` | 游戏区服（可选 `cn`, `en`, `jp`, `tw`） |
| `--frames` | `10` | 每个页面连续采样的帧数 |
| `--interval` | `0.15` | 采样间隔秒数（用于捕获动画与光效抖动） |
| `--threshold` | `0.85` | 模板匹配成功阈值 |
| `--borderline-low` | `0.80` | 危险临界区下界（标记 `0.80 <= 相似度 < 0.85` 的项） |
| `--current-only` | `False` | 仅扫描当前画面，不触发页面跳转导航 |
| `--offline` | `None` | 指定离线本地图片，完全跳过设备连接 |
| `--dump-dir` | `output/template_audit` | 坏帧切片与差值热力图导出目录 |
| `--pages` | 全部默认航线 | 自定义需巡检的 Page 变量名列表 |

---

### 诊断报告指标解读

体检报告包含以下判定状态：
- 🟢 **PASS**：多帧采样下 `Mean >= 0.85` 且 `Min >= 0.85`，模板识别高度稳定。
- 🟡 **BORDERLINE**：相似度处于 `[0.80, 0.85)` 区间，或局部掉线（`Min < 0.85 <= Max`）。**这是导致概率性匹配失败的最主要元凶**！
- 🔴 **FAIL**：相似度 `< 0.80`，未能有效命中。
- ⚪ **ABSENT**：相似度 `< 0.50`，代表该按钮在当前页面正常未显示。

若出现 BORDERLINE 或 FAIL，工具会自动给出根因诊断标签：
1. `红蓝通道疑似反转(BGR>RGB)`：说明数据管线色彩通道反转。
2. `坐标漂移(偏距x:dx,y:dy)`：在扩大搜索区域后找到了高相似度目标，说明按钮在客户端中坐标位移。
3. `帧间抖动剧烈(Std>0.03)`：说明受动态立绘、呼吸光效或粒子干扰。
4. `仅色彩微变(Luma达标)`：说明灰度结构完好，单纯是背景光照微调导致色彩值波动。

---

## English

### Background & Motivation

In an automation environment backed by an Android Virtual Display (1280×720 native), users may occasionally encounter "intermittent template matching failures". Such probabilistic issues are usually not crashes, but rather borderline threshold fluctuations caused by:

1. **Borderline Scores (`0.80~0.85`)**: AzurPilot enforces a strict `0.85` similarity threshold for native 720P resolution. When scenes have subtle particle effects, breathing animations, or in-flight alpha transitions, scores can hover around `0.81~0.84`, failing unpredictably.
2. **Spatial Drift**: UI elements moving slightly after game client layout updates, exceeding the default strict offset window.
3. **Color and Channel Discrepancies**: Channel inversion (BGR vs. RGB) or GPU shader gamut variations.

The automated benchmarking tool `tools/test_template_matching.py` was developed to autonomously navigate through key game pages, perform temporal frame sampling and ablation analysis, and export visual difference heatmaps.

---

### Usage

Run using the `AzurLaneAutoScript` virtual environment:

```bash
# 1. Ensure ADB port forwards
adb forward tcp:22301 tcp:22301
adb forward tcp:25548 tcp:25548

# 2. Autonomous multi-page tour (default)
python tools/test_template_matching.py

# 3. Selected pages only
python tools/test_template_matching.py --pages page_main page_reward page_commission

# 4. Audit current screen without navigation
python tools/test_template_matching.py --current-only

# 5. Offline replay on past failure screenshots
python tools/test_template_matching.py --offline <path_to_image.png>
```
