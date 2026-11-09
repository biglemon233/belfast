#!/usr/bin/env python3
"""首次部署时创建 Android 虚拟屏实例配置，不覆盖用户已有配置。

由 app 侧 ProotHost 在拉起 PRoot 会话前于 guest 内执行（.venv/bin/python
seed_azurpilot.py）：以 config/template.json 为模板生成 config/ap.json 实例，
写入 Android 专属的串口 / 截图 / 控制 / OCR 后端设置；实例已存在则直接返回，
用户配置因此永不被覆盖。

Creates the Android virtual-display instance config on first deployment and
never overwrites an existing user config.

Executed inside the guest by the app-side ProotHost before the PRoot session
starts (.venv/bin/python seed_azurpilot.py): it generates config/ap.json from
config/template.json, writing the Android-specific serial, screenshot, control,
and OCR backend settings. If the instance already exists the script returns
early, so user configuration is never clobbered.
"""

import json
import os
from pathlib import Path
from typing import Any

# 实例配置文件名：上游 module/config/utils.py 的 DEFAULT_CONFIG_NAME（当前为 'ap'），
# 由 filepath_config() 拼成 ./config/<name>.json。要跟随上游改名，只需改这一行。
INSTANCE_FILE = 'config/ap.json'

# 全局段（Emulator / Optimization / Storage 所在的那一节）在上游 schema 里唯一，
# 按结构定位而不写死段名：上游若改段名，这里自动跟上；结构一旦变动，下面的断言会当场报错。
GLOBAL_SECTION_MARKERS = {'Emulator', 'Optimization'}


def global_section(data: dict[str, Any]) -> dict[str, Any]:
    """按结构标记定位配置中的全局段（Emulator / Optimization 所在节）。

    Locates the global section (the one holding Emulator / Optimization) by
    structural markers.

    Args:
        data: 上游模板解析出的完整配置。/ The full config parsed from the
            upstream template.
    Returns:
        唯一匹配的全局段字典。/ The uniquely matched global section dict.
    Raises:
        SystemExit: 匹配段不是恰好一个，说明上游 schema 变动。/ The match count
            is not exactly one, meaning the upstream schema changed.
    """
    found = [
        key for key, value in data.items()
        if isinstance(value, dict) and GLOBAL_SECTION_MARKERS <= set(value)
    ]
    if len(found) != 1:
        raise SystemExit(f'上游 schema 变动：全局段不唯一 {found}')
    return data[found[0]]


def main() -> None:
    """从模板播种 Android 实例配置；实例已存在时幂等跳过。

    Seeds the Android instance config from the template; skips idempotently
    when the instance already exists.

    Raises:
        FileNotFoundError: 模板 config/template.json 缺失。/ The template
            config/template.json is missing.
        SystemExit: 全局段定位失败（上游 schema 变动）。/ The global section
            cannot be located (upstream schema changed).
    """
    root = Path(os.environ.get('AZURPILOT_ROOT', '/opt/azurpilot'))
    source = root / 'config/template.json'
    target = root / INSTANCE_FILE
    if target.exists():
        print('AzurPilot instance already exists')
        return
    data = json.loads(source.read_text(encoding='utf-8'))
    section = global_section(data)
    section['Emulator'].update({
        'Serial': 'azurpilot_android',
        'PackageName': 'com.bilibili.azurlane',
        'ScreenshotMethod': 'azurpilot_android',
        'ControlMethod': 'azurpilot_android',
        'ScreenshotDedithering': False,
    })
    section['Optimization'].update({
        'OcrDevice': 'cpu',
        'OcrBackend': 'onnxruntime',
    })
    target.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print('Seeded AzurPilot for Android instance')


if __name__ == '__main__':
    main()
