#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""AlasAos：运行期校正 config/deploy.yaml 的「策略键」到 App 期望值。

背景：
  deploy.yaml 是构建期烘焙进 config/ 的（build-rootfs.sh install），运行期无覆盖。
  于是改 rootfs/seeds/deploy.yaml 只有重烘焙 rootfs 才生效，而 rootfs 本机烘焙不了
  （要 aarch64 chroot / GHA）。为了「改 deploy 策略键不必重烘焙 rootfs」，本脚本作为
  overlay 资产随 App 版本走（alas/overlay/seeds/），每次启动由 ProotHost 调用，把需要
  App 侧固定的策略键校正到位。

只校正「静态策略键」，**绝不碰 Repository/Branch**——那两键跟随用户换源，由
  alasaos_update.sh::sync_deploy_yaml 动态维护；两边各管各的键、互不打架。

实现用行级正则替换（保留 ALAS poor_yaml 的注释与行结构，不用 PyYAML 回写以免丢注释）。
幂等：已是目标值则不写盘。失败只打印警告、退出 0，绝不阻塞启动。

当前校正项：
  EnableReload: true   —— WebUI「Force restart」需要它（父子进程守护，重启重载代码）
"""
import os
import re
import sys

ALAS = os.environ.get('ALASAOS_ALAS_ROOT', os.path.expanduser('~/alas'))
DEPLOY = os.path.join(ALAS, 'config', 'deploy.yaml')

# key -> 目标值（字符串，原样写进冒号后）。只放静态策略键。
TARGETS = {
    'EnableReload': 'true',
}
theme = os.environ.get('ALASAOS_THEME')
if theme in ('dark', 'default'):
    TARGETS['Theme'] = theme


def main():
    if not os.path.isfile(DEPLOY):
        print(f'seed_deploy: {DEPLOY} not found, skip')
        return 0
    try:
        # newline='' 关闭通用换行转换：CRLF/LF 原样读入，改动行保留原行尾，
        # 未改动行也不被悄悄改写行尾（避免无谓的全文 CRLF->LF diff）
        with open(DEPLOY, encoding='utf-8', newline='') as f:
            text = f.read()
    except Exception as e:  # noqa: BLE001
        print(f'seed_deploy: read failed ({e}), skip')
        return 0

    changed = False
    for key, want in TARGETS.items():
        # 匹配形如「<缩进>EnableReload: <任意旧值><行尾>」，保留缩进与行尾（含 CRLF）
        pattern = re.compile(
            r'^(?P<indent>[ \t]*)' + re.escape(key) + r'(?P<sep>:[ \t]*)(?P<val>.*?)(?P<eol>\r?)$',
            re.MULTILINE,
        )

        def _repl(m, _want=want):
            nonlocal changed
            if m.group('val').strip() == _want:
                return m.group(0)
            changed = True
            return f"{m.group('indent')}{key}{m.group('sep')}{_want}{m.group('eol')}"

        new_text, n = pattern.subn(_repl, text)
        if n == 0:
            print(f'seed_deploy: key {key} not present, skip')
            continue
        text = new_text

    if not changed:
        print('seed_deploy: deploy.yaml already up to date')
        return 0
    try:
        with open(DEPLOY, 'w', encoding='utf-8', newline='') as f:
            f.write(text)
        print(f'seed_deploy: corrected {", ".join(TARGETS)} in deploy.yaml')
    except Exception as e:  # noqa: BLE001
        print(f'seed_deploy: write failed ({e}), skip')
    return 0


if __name__ == '__main__':
    sys.exit(main())
