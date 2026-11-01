<!-- kron:frontmatter -->
id: depth-on-illustration-usable
text: DA3 单目深度在插画（训练分布之外的输入）上能产出可用的体积感，而不是退化成一张纸片。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-01T08:48:06Z"
expires_at: "2026-12-31"
<!-- /kron:frontmatter -->

## 为什么是硬假设

这是整条插画线的前提。如果插画的深度输出不可用，几何点云方案就要退回纯 2.5D 分层视差（不依赖深度几何），
整个技术方案和阶段计划都要改。

## 怎么验证

拿 3 张风格差异大的插画（日式平涂 / 厚涂 / 线稿）跑一遍 DA3MONO，肉眼判断是否有分层与体积感。
必须最先做，且必须在装完环境后立刻做——不要先搭架构。

## 破裂时的应对

退回 2.5D 分层视差：前景/背景两层 + 深度驱动的视差位移，不追求几何正确。
