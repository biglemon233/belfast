"""纯 Python jellyfish shim（Termux 环境专用）。

现代 jellyfish（1.x）是 Rust/maturin 构建，Termux 无法安装；0.11.x 的 C 扩展构建亦不可靠。
ALAS 全库只用到 levenshtein_distance（module/commission/project.py、module/island*）。
由 termux/setup_env.sh 拷入 site-packages；若日后 jellyfish 可正常安装，删除本文件即可。
"""


def levenshtein_distance(s1: str, s2: str) -> int:
    """经典 DP 编辑距离，语义与 jellyfish.levenshtein_distance 一致。"""
    if len(s1) < len(s2):
        s1, s2 = s2, s1
    if not s2:
        return len(s1)
    previous = list(range(len(s2) + 1))
    for i, c1 in enumerate(s1):
        current = [i + 1]
        for j, c2 in enumerate(s2):
            current.append(min(previous[j + 1] + 1, current[j] + 1, previous[j] + (c1 != c2)))
        previous = current
    return previous[-1]
