<!-- kron:frontmatter -->
id: xformers-optional
text: 不安装 xformers 时 DA3 仍可正常推理——model/dinov2/layers/swiglu_ffn.py 里有 ImportError 回退到纯 PyTorch 实现。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-01T08:48:06Z"
verified_at: "2026-11-01"
verified_by: '@zhangjun005'
<!-- /kron:frontmatter -->

## 依据（已读上游源码确认）

src/depth_anything_3/model/dinov2/layers/swiglu_ffn.py：

    try:
        from xformers.ops import SwiGLU
        XFORMERS_AVAILABLE = True
    except ImportError:
        SwiGLU = SwiGLUFFN
        XFORMERS_AVAILABLE = False

## 为什么要记这条

把 xformers 从依赖里去掉，就避开了 Windows 上"装 xformers 把 torch 反向改版本"的坑。
虽然 pyproject.toml 把 xformers 列为硬依赖，但运行时有回退，所以可以不装。

## 待办

仍需实际跑一次推理确认走的是回退路径。
