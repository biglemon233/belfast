#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""AlasAos：由 config/template.json 生成 config/alas.json 并注入桥接配置。

在 ~/alas 根目录下运行：python seed_config.py
幂等：已有用户配置时不再生成默认项（改配置请走 WebUI）。
"""
import json
import os
import sys
from pathlib import Path

ROOT = os.path.dirname(os.path.abspath(__file__))
# 允许从任意目录运行：脚本在 termux/ 下，ALAS 根目录取 ~/alas
ALAS = os.environ.get('ALASAOS_ALAS_ROOT', os.path.expanduser('~/alas'))

SRC = os.path.join(ALAS, 'config', 'template.json')
DST = os.path.join(ALAS, 'config', 'alas.json')

OVERRIDES = {
    # 只注入「桥接必需」项；PackageName / ScreenshotDedithering 交给 ALAS 自己管理：
    # template 默认 PackageName='auto'（自动识别游戏包名，含渠道服），Dedithering 默认 False。
    ('Alas', 'Emulator', 'Serial'): 'alasaos',
    ('Alas', 'Emulator', 'ScreenshotMethod'): 'alasaos',
    ('Alas', 'Emulator', 'ControlMethod'): 'alasaos',
}


def main():
    if os.path.exists(DST):
        print(f'{DST} already exists, skip')
        return
    if any(not path.stem.startswith('template') for path in Path(ALAS, 'config').glob('*.json')):
        print('User configurations already exist, skip default seed')
        return
    with open(SRC, encoding='utf-8') as f:
        cfg = json.load(f)
    for (menu, group, arg), value in OVERRIDES.items():
        cfg[menu][group][arg] = value
    with open(DST, 'w', encoding='utf-8') as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
    print(f'seeded {DST} with alasaos bridge config')


if __name__ == '__main__':
    sys.exit(main())
