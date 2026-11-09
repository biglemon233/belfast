#!/usr/bin/env python3
"""在 EGL 执行生产计算着色器，与稳定 CPU Softmax 对照。

开发环境允许 Mesa 软件渲染来验证着色器语义；App 会拒绝软件渲染器。
此测试不提供 Android GPU、NPU 或速度证明。需要 PyOpenGL、NumPy 和 EGL/GLES。

Executes the production shader through EGL against stable CPU Softmax. Developer tests
allow Mesa software rendering for shader semantics; the app rejects software renderers.
Does not prove Android GPU/NPU use or speed. Requires PyOpenGL, NumPy, and EGL/GLES.
"""

import ctypes
import os
from pathlib import Path
import textwrap

os.environ.setdefault('PYOPENGL_PLATFORM', 'egl')
os.environ.setdefault('EGL_PLATFORM', 'surfaceless')

import numpy as np
from OpenGL import EGL
from OpenGL import GL as gl
from OpenGL.GL.shaders import compileShader, compileProgram


def main():
    """校验大字典、极端 logits 和均匀分布。 / Checks large dictionaries, extreme logits, and uniform rows."""
    root = Path(__file__).resolve().parents[2]
    source = (root / 'app/app/src/main/kotlin/com/azurpilot/ghio/ocr/OcrGpuSoftmax.kt').read_text()
    shader = textwrap.dedent(source.split('val SHADER = """', 1)[1].split('""".trimIndent()', 1)[0]).strip()
    display = EGL.eglGetDisplay(EGL.EGL_DEFAULT_DISPLAY)
    assert EGL.eglInitialize(display, None, None)
    configs = (EGL.EGLConfig * 1)()
    count = ctypes.c_int()
    attrs = (ctypes.c_int * 5)(EGL.EGL_RENDERABLE_TYPE, 0x40, EGL.EGL_SURFACE_TYPE, EGL.EGL_PBUFFER_BIT, EGL.EGL_NONE)
    assert EGL.eglChooseConfig(display, attrs, configs, 1, count) and count.value
    context_attrs = (ctypes.c_int * 3)(EGL.EGL_CONTEXT_CLIENT_VERSION, 3, EGL.EGL_NONE)
    context = EGL.eglCreateContext(display, configs[0], EGL.EGL_NO_CONTEXT, context_attrs)
    surface_attrs = (ctypes.c_int * 5)(EGL.EGL_WIDTH, 1, EGL.EGL_HEIGHT, 1, EGL.EGL_NONE)
    surface = EGL.eglCreatePbufferSurface(display, configs[0], surface_attrs)
    assert EGL.eglMakeCurrent(display, surface, surface, context)
    dispatch = ctypes.CFUNCTYPE(None, ctypes.c_uint, ctypes.c_uint, ctypes.c_uint)(EGL.eglGetProcAddress(b'glDispatchCompute'))
    barrier = ctypes.CFUNCTYPE(None, ctypes.c_uint)(EGL.eglGetProcAddress(b'glMemoryBarrier'))
    print(f"SHADER_TEST_RENDERER {gl.glGetString(gl.GL_RENDERER)} {gl.glGetString(gl.GL_VERSION)}", flush=True)
    program = compileProgram(compileShader(shader, 0x91B9))
    buffers = gl.glGenBuffers(2)
    try:
        for classes in (97, 18385, 18710):
            values = np.random.default_rng(20261009).uniform(-100, 100, (40, classes)).astype(np.float32)
            values[0] = 0
            values[1] = -1000
            values[1, 0] = 1000
            expected = np.exp(values.astype(np.float64) - values.max(-1, keepdims=True))
            expected /= expected.sum(-1, keepdims=True)
            for index, buffer in enumerate(buffers):
                gl.glBindBuffer(0x90D2, int(buffer))
                gl.glBufferData(0x90D2, values.nbytes, values if index == 0 else None, gl.GL_STREAM_DRAW)
                gl.glBindBufferBase(0x90D2, index, int(buffer))
            gl.glUseProgram(program)
            gl.glUniform1i(gl.glGetUniformLocation(program, 'classes'), classes)
            dispatch(values.shape[0], 1, 1)
            barrier(0x200 | 0x2000)
            address = gl.glMapBufferRange(0x90D2, 0, values.nbytes, gl.GL_MAP_READ_BIT)
            assert address
            result = np.frombuffer(ctypes.string_at(address, values.nbytes), np.float32).reshape(values.shape).copy()
            assert gl.glUnmapBuffer(0x90D2)
            assert gl.glGetError() == gl.GL_NO_ERROR
            np.testing.assert_allclose(result, expected, rtol=2e-4, atol=1e-7)
            np.testing.assert_allclose(result.sum(-1), 1, atol=1e-5)
            np.testing.assert_array_equal(result.argmax(-1), expected.argmax(-1))
            print(f'GPU_SHADER_OK classes={classes} max_abs_error={np.max(np.abs(result-expected)):.8g}', flush=True)
    finally:
        gl.glDeleteBuffers(2, buffers)
        gl.glDeleteProgram(program)
        EGL.eglMakeCurrent(display, EGL.EGL_NO_SURFACE, EGL.EGL_NO_SURFACE, EGL.EGL_NO_CONTEXT)
        EGL.eglDestroySurface(display, surface)
        EGL.eglDestroyContext(display, context)
        EGL.eglTerminate(display)


if __name__ == '__main__':
    main()
