<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:59:18.6145327Z
status: active
<!-- /kron:frontmatter -->
# 深度 Sobel 描边

> 描边 pass 用平滑后的深度图做 Sobel，把深度不连续处叠成墨线，参数 edgeStrength / edgeThreshold / edgeWidth。

它在彩色点画预设里默认关闭（edgeStrength=0），因为线稿已经存在于每点颜色里，再描边只会加粗成黑边；在单色/版画路径里才有用。
