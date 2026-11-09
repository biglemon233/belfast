"""numpy 2.x 兼容垫片：补回 mxnet 1.9.1/cnocr 1.2.2 依赖的旧别名（ALAS 原版链用）。"""
try:
    import numpy as _np
    _ALIASES = {
        'PZERO': 0.0, 'NZERO': -0.0,
        'long': int, 'ulong': _np.uint64,
        'int': int, 'float': float, 'bool': bool, 'object': object,
        'str': str, 'unicode': str, 'complex': complex,
        'Inf': _np.inf, 'Infinity': _np.inf, 'infty': _np.inf,
        'NINF': -_np.inf, 'PINF': _np.inf, 'NaN': _np.nan, 'NAN': _np.nan,
        'asscalar': lambda a: a.item(),
    }
    for _n, _v in _ALIASES.items():
        if not hasattr(_np, _n):
            try:
                setattr(_np, _n, _v)
            except Exception:
                pass
except Exception:
    pass
