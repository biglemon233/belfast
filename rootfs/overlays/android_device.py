"""将 Android 前台应用检测限定在当前虚拟屏，不受主屏焦点影响。

在上游设备模块导入后替换解析器；APK 同步此兼容层，已有 Runtime 和源码热更均可使用。

Scopes Android app detection to the current virtual display independently of primary focus.

Replaces the parser after the upstream device module imports. APK synchronization supports
existing runtimes and source updates.
"""

import importlib.abc
import importlib.machinery
import re
import sys
import time


TARGET_MODULE = 'module.device.method.azurpilot_android'
DISPLAY_HEADER = re.compile(r'(?m)^[ \t]*Display(?:[ \t]+|:)[^\r\n]*')
HEADER_ID = re.compile(r'\b(?:mDisplayId|displayId)[ \t]*=[ \t]*(\d+)\b')
NUMBERED_HEADER = re.compile(r'^Display[ \t]+#?(\d+)\b')
COMPONENT = re.compile(r'(?<![\w.])([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)/[.\w$]+')


def display_section(output, display_id):
    """返回精确匹配 ID 的显示器段；未知标题仍作为分段边界，避免串到其它屏。

    Returns the section with an exact display ID. Unknown headers still delimit sections
    so parsing cannot spill into another display.
    """
    headers = list(DISPLAY_HEADER.finditer(output))
    for index, header in enumerate(headers):
        title = header.group().strip()
        match = HEADER_ID.search(title) or NUMBERED_HEADER.search(title)
        if match and int(match.group(1)) == display_id:
            end = headers[index + 1].start() if index + 1 < len(headers) else len(output)
            return output[header.end():end]
    return ''


def focused_package(output, display_id, activity=False):
    """只从目标屏的焦点或已恢复 Activity 中提取包名；没有匹配时返回空串。

    Extracts a package only from the target display's focus or resumed Activity, returning
    an empty string when none matches.
    """
    section = display_section(output, display_id)
    fields = ('mResumedActivity', 'Resumed', 'topResumedActivity') if activity else (
        'mCurrentFocus', 'mFocusedWindow', 'mFocusedApp')
    for field in fields:
        line = re.search(r'(?m)^[ \t]*' + field + r'[ \t]*[:=][ \t]*([^\r\n]+)', section)
        if line:
            match = COMPONENT.search(line.group(1))
            if match:
                return match.group(1)
    return ''


def patch_ap_module(module):
    """幂等替换 Android 前台检测，保留启动、截图及其它设备后端。

    Replaces Android app detection idempotently while retaining launch, capture, and other backends.
    """
    backend = module.AzurPilotAndroid
    if getattr(backend, '_android_display_adapter', False):
        return

    def app_current(self, timeout=30):
        """在总查询预算内读取当前虚拟屏；无窗口焦点时查询该屏的已恢复 Activity。

        Reads the current virtual display within a total query budget, using its resumed Activity
        when window focus is unavailable.
        """
        deadline = time.monotonic() + timeout
        display_id = self.azurpilot_android_display_id
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return ''
        output = self.azurpilot_android_shell_output('dumpsys window displays', timeout=remaining)
        package = focused_package(output, display_id)
        remaining = deadline - time.monotonic()
        if not package and remaining > 0:
            # 副屏可保持游戏运行而不持有全局输入焦点，不能据此判定游戏已崩溃。
            output = self.azurpilot_android_shell_output('dumpsys activity activities', timeout=remaining)
            package = focused_package(output, display_id, activity=True)
        module.logger.attr('App current (azurpilot_android)', package)
        return package

    backend.app_current_azurpilot_android = app_current
    backend._android_display_adapter = True


class _Loader(importlib.abc.Loader):
    """在原模块初始化后安装兼容层。 / Installs the adapter after original module initialization."""

    def __init__(self, original):
        self.original = original

    def create_module(self, spec):
        return self.original.create_module(spec)

    def exec_module(self, module):
        self.original.exec_module(module)
        patch_ap_module(module)


class _Finder(importlib.abc.MetaPathFinder):
    """仅拦截 Android 设备后端。 / Intercepts only the Android device backend."""

    _android_display_adapter = True

    def find_spec(self, fullname, path, target=None):
        if fullname != TARGET_MODULE:
            return None
        spec = importlib.machinery.PathFinder.find_spec(fullname, path)
        if spec and spec.loader:
            spec.loader = _Loader(spec.loader)
        return spec


def install():
    """延迟安装解析器；已导入模块立即接线，多次调用只注册一次。

    Installs lazily, patches already loaded modules, and registers the finder only once.
    """
    module = sys.modules.get(TARGET_MODULE)
    if module is not None:
        patch_ap_module(module)
    if not any(getattr(finder, '_android_display_adapter', False) for finder in sys.meta_path):
        sys.meta_path.insert(0, _Finder())
