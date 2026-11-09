#!/usr/bin/env python3
# =============================================================================
# AlasAos · args 现场再生（proot 内由 App 侧 ProotHost 每次启动拉起）
#
# 为什么存在：module/config/argument/{args.json,argument.yaml} 曾是整文件补丁，
# cp -rf 重放把活动列表冻回烘焙日（「星光之城」案，与 base.py 冻结同源同病）。
# 现这两个文件不再补丁化——上游 git 跟踪它们，热更新自动带新；本脚本每次启动
# 现场跑 ALAS 完整生成链（活动列表随 campaign/Readme.md 走），再补回 AlasAos 桥选项。
#
# 步骤：
#   1) python -m module.config.config_updater  → menu/args/config_generated/i18n/template
#   2) args.json: Alas.Emulator.{ScreenshotMethod,ControlMethod}.option 补 'alasaos'
#   3) module/config/i18n/zh-CN.json: 同上两节点补 "alasaos" 显示名
#
# 全部幂等。协议：成功 exit 0；生成链失败 exit 1（App 降级为警告，不阻塞启动）。
# 注意：args.json / zh-CN.json 被本脚本改写后工作区是脏的，但热更新走
# git reset --hard，不Care本地改动。
# =============================================================================
import json
import os
import subprocess
import sys
import time

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))  # seeds/ 的上级 = /opt/alas
ARGS_JSON = os.path.join(BASE, 'module', 'config', 'argument', 'args.json')
I18N_ZH = os.path.join(BASE, 'module', 'config', 'i18n', 'zh-CN.json')

ALASAOS_OPTION_ARGS = [
    ('Alas', 'Emulator', 'ScreenshotMethod'),
    ('Alas', 'Emulator', 'ControlMethod'),
]
ALASAOS_DISPLAY = 'ALAS-AOS 桥'
GENERATE_TIMEOUT_SEC = 600


def _load_json(path):
    with open(path, encoding='utf-8') as f:
        return json.load(f)


def _dump_json(path, data):
    with open(path, 'w', encoding='utf-8', newline='\n') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write('\n')


def main():
    t0 = time.time()

    # 1) ALAS 完整生成链（其 __main__ 自己 chdir 到 alas 根；仍显式给 cwd 双保险）
    # 必须用 -m 模块方式跑：直接传脚本路径时 sys.path[0]=module/config/，
    # `from deploy.utils import ...` 会 ModuleNotFoundError；-m 让 sys.path[0]=cwd=/opt/alas
    r = subprocess.run(
        [sys.executable, '-m', 'module.config.config_updater'],
        cwd=BASE,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=GENERATE_TIMEOUT_SEC,
    )
    out = r.stdout.decode('utf-8', errors='replace')
    if r.returncode != 0:
        print(f'regen_args: generator exit={r.returncode}\n{out[-3000:]}', file=sys.stderr)
        return 1
    for line in out.strip().splitlines()[-3:]:
        print(f'regen_args| {line}', flush=True)

    # 2) args.json 补 alasaos 选项
    args = _load_json(ARGS_JSON)
    patched = []
    for task, group, arg in ALASAOS_OPTION_ARGS:
        node = args.get(task, {}).get(group, {}).get(arg)
        if not isinstance(node, dict):
            print(f'regen_args: WARN {task}.{group}.{arg} missing in args.json', flush=True)
            continue
        opts = node.get('option')
        if isinstance(opts, list) and 'alasaos' not in opts:
            opts.append('alasaos')
            patched.append(f'{task}.{group}.{arg}')
    if patched:
        _dump_json(ARGS_JSON, args)

    # 3) zh-CN.json 补显示名（生成链已重写过 i18n，补在其后；
    #    generate_i18n 会为未知选项留下 `"alasaos": "alasaos"` 占位——不等于显示名时也升级）
    #    注意 i18n 顶层是**组名**（无任务层）：zh['Emulator']['ScreenshotMethod']['alasaos']
    i18n_patched = False
    try:
        zh = _load_json(I18N_ZH)
        for task, group, arg in ALASAOS_OPTION_ARGS:
            node = zh.get(group, {}).get(arg)
            if isinstance(node, dict) and node.get('alasaos') != ALASAOS_DISPLAY:
                node['alasaos'] = ALASAOS_DISPLAY
                i18n_patched = True
        if i18n_patched:
            _dump_json(I18N_ZH, zh)
    except OSError as e:
        print(f'regen_args: WARN i18n patch skipped: {e}', flush=True)

    print(f'regen_args: done in {time.time() - t0:.1f}s '
          f'options_patched={patched or "already"} i18n_patched={i18n_patched}', flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
