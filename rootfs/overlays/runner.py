#!/usr/bin/env python3
"""AlasAos v3 · ALAS 薄 runner（rootfs 内，由 wrapper.py 以子进程方式拉起）。

职责只有三件：
1. 把 CWD 钉在脚本所在目录（= ALAS 根；ALAS 所有路径都是 './...' 相对路径，
   module/logger.py import 期还会再 chdir 一次兜底，见 logger.py:165）。
2. import ALAS 并按 argv 跑任务：from alas import AzurLaneAutoScript
   （官方无头入口 alas.py:621-623 的同款调用；注意类在 alas.py 里，
   不是 AzurLaneAutoScript.py——仓库里没有这个文件）。
   - 无 task：alas.loop() 调度器主循环（挂机）。
   - task ∈ {daemon, event_story}：alas.run(task, skip_first_screenshot=True)
     无头跑单个工具任务（daemon=半自动点击常驻无限循环，event_story=活动剧情
     一次性约 3 分钟自退）；与挂机共用同一块虚拟屏，互斥由 wrapper 侧保证
     （/tool/start 先停 runner），本文件只负责按 argv 执行。
     例外：daemon 前先跑一次官方 start 任务拉起游戏+过登录——daemon 本身只盯屏
     不拉游戏（上游设计），直接进循环会对着黑帧空转。
   - task 其他值：打印用法并以非零码退出。
3. 顶层异常落 ./log/wrapper_runner_error.txt 并以非零码退出，供 wrapper 判死。

用法：runner.py <config> [task]。实例名默认 'alas'（= AzurLaneAutoScript() 默认值），
可用 argv[1] 或环境变量 ALASAOS_ALAS_CONFIG 覆盖。日志文件随之是
./log/{YYYY-MM-DD}_{实例名}.txt（logger.py:171-177）。

停止语义：不装 SIGTERM handler——ALAS 本身没有（m0 ProcessManager.stop 就是 SIGKILL），
优雅停会被 loop() 的当前任务拖住；由 wrapper 侧 SIGTERM→3s→SIGKILL 杀进程组，等同 m0。
"""
import os
import sys
import traceback

# CWD = 脚本所在目录（ALAS 根）。必须在任何 module.* import 之前完成。
ALAS_ROOT = os.path.dirname(os.path.abspath(__file__))
os.chdir(ALAS_ROOT)
if ALAS_ROOT not in sys.path:
    sys.path.insert(0, ALAS_ROOT)


# 工具任务白名单：与 wrapper.py 的 _TOOL_TASKS 对齐（两处同改）
_TOOL_TASKS = ('daemon', 'event_story')


def main():
    config_name = sys.argv[1] if len(sys.argv) > 1 else os.environ.get('ALASAOS_ALAS_CONFIG', 'alas')
    task = sys.argv[2] if len(sys.argv) > 2 else None
    if task is not None and task not in _TOOL_TASKS:
        print(f'usage: runner.py <config> [{"|".join(_TOOL_TASKS)}]  # task 省略=挂机 loop',
              file=sys.stderr)
        return 2
    from module.logger import logger  # 可选；import 副作用 = chdir 兜底 + 建 ./log/{date}_runner.txt
    logger.info(f'AlasAos runner: start AzurLaneAutoScript(config_name={config_name!r}, task={task!r})')

    from alas import AzurLaneAutoScript
    alas = AzurLaneAutoScript(config_name=config_name)
    if task is None:
        # 进调度前先清无窗孤儿：App 重装/VD 重建后游戏进程可能仍被 pidof 判活，
        # 但窗口已随旧 VD 销毁、新 VD 上只出纯黑帧。ALAS 只按 pid 判活
        # （ui.py GameNotRunningError 不会触发），ui_ensure 拿黑帧直接
        # GamePageUnknownError → exit(1)，wrapper 重拉后孤儿依旧 → 死循环。
        # 此处先按 daemon 同款黑帧判定（check_screen_black：全屏通道均值和 <1）
        # 把孤儿 app_stop，让调度器走 GameNotRunningError → task_call('Restart')
        # 由官方 Restart 任务把游戏重新拉起到 VD 上。画面健在则不动。
        if alas.device.app_is_running():
            alas.device.screenshot()
            image = alas.device.image
            if image is None or float(image.mean()) * 3 < 1.0:
                logger.warning('AlasAos runner: orphan game (pid alive, black frames), '
                               'app_stop before loop')
                alas.device.app_stop()
        alas.loop()  # 阻塞；内部 set_file_logger(config_name)，正常退出/崩溃均有 exit(1) 分支
    else:
        # daemon（半自动点击）上游设计上不拉游戏——官方前提是游戏已在跑，它只盯当前屏。
        # 本机一体场景用户按一下就期望全包：游戏没在跑时先复用官方 start 任务拉起+过登录
        # （LoginHandler.app_start），失败则不裸进盯屏循环。
        # 但游戏**已在跑时绝不跑 start**：start 的语义是"拉到主界面"（handle_app_login 末尾
        # ui_goto(page_main)），会把已停在活动图/关卡页等准备好位置的用户硬拽回主界面；
        # 此时直接进 daemon 盯屏循环，当前屏在哪就在哪工作（上游设计本意）。
        # 注意"进程活着≠画面活着"：App 重装/VD 重建后游戏可能是无窗孤儿进程
        # （pidof 判活但 VD 黑帧），此时仍要走 start 把活动重新拉起到 VD 上。
        # 黑帧判定对齐 ALAS check_screen_black：全屏通道均值和 <1（screenshot.py:247）。
        # event_story 不需要这层——它 run() 内部自带 app_start（eventstory.py:214）。
        if task == 'daemon':
            need_start = True
            if alas.device.app_is_running():
                alas.device.screenshot()
                image = alas.device.image
                if image is not None and float(image.mean()) * 3 >= 1.0:
                    need_start = False
                    logger.info('AlasAos runner: game already on screen, skip daemon pre-start')
                else:
                    logger.warning('AlasAos runner: game process alive but frames black, '
                                   'treat as not running')
            if need_start and not alas.run('start', skip_first_screenshot=True):
                logger.error('AlasAos runner: daemon pre-start (app_start) failed, abort tool')
                return 1
        alas.run(task, skip_first_screenshot=True)  # 工具任务：一次性/常驻，由 wrapper 保证与挂机互斥
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except SystemExit:
        raise
    except BaseException:
        # ALAS 自己 crash 时会 exit(1)（走上面的 SystemExit 分支）；
        # 落到这里的只有 import 期/启动期的致命错误，ALAS 日志体系可能还没建好，单独落文件。
        os.makedirs('./log', exist_ok=True)
        with open('./log/wrapper_runner_error.txt', 'a', encoding='utf-8') as f:
            f.write(traceback.format_exc() + '\n')
        sys.exit(1)
