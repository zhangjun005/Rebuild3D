<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:56.8668416Z
status: active
<!-- /kron:frontmatter -->
# C 盘预算：缓存与模型全部落 E 盘

> C 盘只有 ~3.9 GB，一次 torch wheel 解包就把它从 3.8 GB 压到 1.11 GB。

规则：所有缓存与模型都落 E 盘——TMP/TEMP/TMPDIR=E:\cache\tmp、PIP_CACHE_DIR=E:\cache\pip、HF_HOME=E:\models\hf，模型本体在 E:\models\{anime-seg,lama}。任何"装个包/下个模型"的动作先想清楚落盘位置。
