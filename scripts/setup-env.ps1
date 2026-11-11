# @kron:intent env/conda-isolation
# @kron:intent env/disk-budget
<#
.SYNOPSIS
    建立 illust3d 的隔离环境。幂等，可反复运行。

.DESCRIPTION
    按 docs/selection.md §1 执行，绝不触碰 conda base / 全局 Python：

      1) 在 E 盘建 conda env（.condarc 已把 envs_dirs 指向 E:\conda\envs_dirs）
      2) 写 activate.d/env_vars.{ps1,bat}：pip 与 HF/torch 缓存重定向到 E 盘，
         并设两道硬闸（PIP_REQUIRE_VIRTUALENV / PYTHONNOUSERSITE）
      3) 装 CUDA 版 torch + sidecar 最小依赖
      4) vendor Depth-Anything-3 源码（固定 commit）并打懒加载补丁

    不装：xformers、trimesh、open3d、pycolmap、evo、moviepy、gsplat（见 §1.4）

.PARAMETER EnvName
    conda 环境名，默认 illust3d

.PARAMETER PythonVersion
    默认 3.10（与已验证可用的 3d-build 同版本）

.PARAMETER DownloadModel
    给一个 HF repo id 就顺带把权重下到 HF_HOME（例如 depth-anything/DA3MONO-LARGE）

.EXAMPLE
    pwsh -NoProfile -File scripts/setup-env.ps1
    pwsh -NoProfile -File scripts/setup-env.ps1 -DownloadModel depth-anything/DA3MONO-LARGE
#>
[CmdletBinding()]
param(
    [string]$EnvName = 'illust3d',
    [string]$PythonVersion = '3.10',
    [string]$TorchVersion = '2.5.1',
    [string]$TorchVisionVersion = '0.20.1',
    [string]$CuTag = 'cu121',
    [string]$VendorCommit = '',
    [string]$DownloadModel = '',
    [switch]$SkipPackages
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# ── 路径常量 ──────────────────────────────────────────────────────────────
$RepoRoot    = Split-Path -Parent $PSScriptRoot
$CondaBat    = 'E:\CondaBody\Library\bin\conda.bat'
$EnvsDir     = 'E:\conda\envs_dirs'
$EnvDir      = Join-Path $EnvsDir $EnvName
$EnvPy       = Join-Path $EnvDir 'python.exe'
$ActivateDir = Join-Path $EnvDir 'etc\conda\activate.d'
$VendorDir   = Join-Path $RepoRoot 'sidecar\vendor\Depth-Anything-3'
$CachePip    = 'E:\cache\pip'
$CacheTmp    = 'E:\cache\tmp'
$ModelsDir   = 'E:\models'
$HfHome      = Join-Path $ModelsDir 'hf'
$TorchHome   = Join-Path $ModelsDir 'torch'

function Step([string]$msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }
function Ok([string]$msg)   { Write-Host "  ok  $msg" -ForegroundColor Green }
function Warn([string]$msg) { Write-Host "  !!  $msg" -ForegroundColor Yellow }

# ── 0. 前置检查 ───────────────────────────────────────────────────────────
Step '前置检查'
foreach ($p in @($CondaBat)) {
    if (-not (Test-Path $p)) { throw "找不到 conda：$p" }
}
if (-not (Test-Path $EnvsDir)) { throw "找不到 envs 目录：$EnvsDir（检查 .condarc 的 envs_dirs）" }
Ok "conda: $CondaBat"
Ok "envs_dirs: $EnvsDir"

$free = (Get-PSDrive E).Free / 1GB
Write-Host ("  E 盘剩余 {0:N1} GB" -f $free)
if ($free -lt 12) { throw "E 盘空间不足 12 GB，先清理再跑" }

# C 盘护栏：本脚本的所有下载都必须落 E 盘，但不小心就会漏。
# 实测踩过：只重定向 PIP_CACHE_DIR 不够，pip 的「临时解包目录」仍走 %TEMP%，
# 2.4 GB 的 torch 轮子直接把 C 盘从 3.8 GB 吃到 1.1 GB。
$freeC = (Get-PSDrive C).Free / 1GB
Write-Host ("  C 盘剩余 {0:N1} GB" -f $freeC)
if ($freeC -lt 2.5) { Warn "C 盘只剩 $([math]::Round($freeC,1)) GB，跑完请尽快清理；下面会把 TEMP 也指向 E 盘" }

# 关键：把临时目录一起搬到 E 盘。pip / huggingface_hub / torch 都会用它。
New-Item -ItemType Directory -Path $CacheTmp -Force | Out-Null
$env:TMP = $CacheTmp
$env:TEMP = $CacheTmp
$env:TMPDIR = $CacheTmp
Ok "TEMP/TMP/TMPDIR -> $CacheTmp"

# ── 1. 建 conda env ───────────────────────────────────────────────────────
Step "conda env: $EnvName"
if (Test-Path $EnvPy) {
    Ok "已存在，跳过创建（$EnvPy）"
} else {
    & $CondaBat create -n $EnvName "python=$PythonVersion" -y
    if ($LASTEXITCODE -ne 0) { throw "conda create 失败" }
    if (-not (Test-Path $EnvPy)) { throw "创建后仍找不到 $EnvPy（env 可能落到了别的 envs_dirs）" }
    Ok "已创建 $EnvPy"
}
$pyVer = & $EnvPy -c "import sys;print(sys.version.split()[0])"
Ok "python $pyVer"

# 硬闸：确认这个解释器确实落在 conda envs 目录下，防止误用 base / 系统 python。
# 注意：不能用 PIP_REQUIRE_VIRTUALENV —— pip 只把 venv 认作虚拟环境，
# conda env 里 sys.prefix == sys.base_prefix，那样设会让所有 pip 调用直接报错退出。
$prefix = (& $EnvPy -c "import sys;print(sys.prefix)").Trim()
if (-not $prefix.ToLower().StartsWith('e:\conda\envs_dirs')) {
    throw "拒绝继续：解释器不在 conda envs 目录下（sys.prefix=$prefix）"
}
Ok "sys.prefix = $prefix"

# ── 2. activate.d：把缓存赶出 C 盘 + 两道硬闸 ─────────────────────────────
Step 'activate.d 环境变量'
New-Item -ItemType Directory -Path $ActivateDir -Force | Out-Null
New-Item -ItemType Directory -Path $CachePip -Force | Out-Null
New-Item -ItemType Directory -Path $CacheTmp -Force | Out-Null
New-Item -ItemType Directory -Path $HfHome -Force | Out-Null
New-Item -ItemType Directory -Path $TorchHome -Force | Out-Null

$ps1 = @"
# 由 scripts/setup-env.ps1 生成，勿手改。
# 目标：不污染全局环境 + 所有缓存/临时文件落 E 盘（C 盘只剩 ~2.5GB）
`$env:PIP_CACHE_DIR          = '$CachePip'
`$env:PYTHONNOUSERSITE       = '1'
`$env:TMP                    = '$CacheTmp'
`$env:TEMP                   = '$CacheTmp'
`$env:TMPDIR                 = '$CacheTmp'
`$env:HF_HOME                = '$HfHome'
`$env:HF_ENDPOINT            = 'https://hf-mirror.com'
`$env:HF_HUB_DISABLE_XET     = '1'
`$env:TORCH_HOME             = '$TorchHome'
"@
$bat = @"
@echo off
rem 由 scripts/setup-env.ps1 生成，勿手改。
set "PIP_CACHE_DIR=$CachePip"
set "PYTHONNOUSERSITE=1"
set "TMP=$CacheTmp"
set "TEMP=$CacheTmp"
set "TMPDIR=$CacheTmp"
set "HF_HOME=$HfHome"
set "HF_ENDPOINT=https://hf-mirror.com"
set "HF_HUB_DISABLE_XET=1"
set "TORCH_HOME=$TorchHome"
"@
Set-Content -Path (Join-Path $ActivateDir 'env_vars.ps1') -Value $ps1 -Encoding utf8
Set-Content -Path (Join-Path $ActivateDir 'env_vars.bat') -Value $bat -Encoding ascii
Ok "已写入 $ActivateDir"

# 本进程也应用一遍，后面的 pip 才会用 E 盘缓存
# 注意：这里刻意不设 PIP_REQUIRE_VIRTUALENV —— pip 不认 conda env，设了会让 pip 拒绝执行。
$env:PIP_CACHE_DIR = $CachePip
$env:PYTHONNOUSERSITE = '1'
$env:TMP = $CacheTmp
$env:TEMP = $CacheTmp
$env:TMPDIR = $CacheTmp
$env:HF_HOME = $HfHome
$env:HF_ENDPOINT = 'https://hf-mirror.com'
$env:TORCH_HOME = $TorchHome

# ── 3. 安装依赖 ───────────────────────────────────────────────────────────
if ($SkipPackages) {
    Warn '跳过依赖安装（-SkipPackages）'
} else {
    Step "torch $TorchVersion+$CuTag"
    & $EnvPy -m pip install --no-input --disable-pip-version-check --progress-bar off `
        "torch==$TorchVersion" "torchvision==$TorchVisionVersion" `
        --index-url "https://download.pytorch.org/whl/$CuTag"
    if ($LASTEXITCODE -ne 0) { throw '安装 torch 失败' }
    Ok 'torch 安装完成'

    Step 'sidecar 最小依赖'
    $req = Join-Path $RepoRoot 'sidecar\requirements.txt'
    & $EnvPy -m pip install --no-input --disable-pip-version-check --progress-bar off -r $req
    if ($LASTEXITCODE -ne 0) { throw '安装 sidecar 依赖失败' }
    Ok 'sidecar 依赖安装完成'

    Step 'CUDA 可用性'
    & $EnvPy -c "import torch;print('torch',torch.__version__,'cuda',torch.cuda.is_available(),torch.cuda.get_device_name(0) if torch.cuda.is_available() else '')"
    if ($LASTEXITCODE -ne 0) { throw 'torch 自检失败' }
}

# ── 4. vendor Depth-Anything-3 ────────────────────────────────────────────
Step 'vendor Depth-Anything-3'
if (Test-Path (Join-Path $VendorDir '.git')) {
    Ok "已存在，跳过 clone（$VendorDir）"
    Push-Location $VendorDir
    try { $head = (git rev-parse HEAD).Trim() } finally { Pop-Location }
} else {
    New-Item -ItemType Directory -Path (Split-Path -Parent $VendorDir) -Force | Out-Null
    git clone --depth 1 https://github.com/ByteDance-Seed/Depth-Anything-3 $VendorDir
    if ($LASTEXITCODE -ne 0) { throw 'clone DA3 失败' }
    Push-Location $VendorDir
    try {
        if ($VendorCommit) {
            git fetch --depth 1 origin $VendorCommit
            if ($LASTEXITCODE -eq 0) { git checkout --detach FETCH_HEAD | Out-Null }
            else { Warn "取不到 commit $VendorCommit，退回 main HEAD" }
        }
        $head = (git rev-parse HEAD).Trim()
    } finally { Pop-Location }
    Ok "已 clone，HEAD=$head"
}
Set-Content -Path (Join-Path $RepoRoot 'sidecar\VENDOR_COMMIT.txt') -Value @"
Depth-Anything-3
repo  : https://github.com/ByteDance-Seed/Depth-Anything-3
commit: $head
note  : 由 scripts/setup-env.ps1 固定；升级时删除 sidecar/vendor/Depth-Anything-3 重新运行。
"@ -Encoding utf8
Ok "写 sidecar/VENDOR_COMMIT.txt"

# ── 5. 懒加载补丁（绕开 trimesh/open3d/pycolmap/moviepy/gsplat）──────────
Step '打补丁：api.py 的 export 改懒加载'
$apiPy = Join-Path $VendorDir 'src\depth_anything_3\api.py'
if (-not (Test-Path $apiPy)) { throw "找不到 $apiPy" }
$src = Get-Content -LiteralPath $apiPy -Raw -Encoding utf8

if ($src -match '# PATCH\(illust3d\)') {
    Ok '已打过补丁，跳过'
} else {
    Copy-Item $apiPy "$apiPy.orig" -Force

    $topImport = 'from depth_anything_3.utils.export import export'
    if ($src -notmatch [regex]::Escape($topImport)) {
        throw 'api.py 里找不到顶层 export import，上游结构变了，需人工处理'
    }
    $src = $src.Replace($topImport, '# PATCH(illust3d): export 改为懒加载，避免拖入 gs/colmap/glb 依赖')

    $call = 'export(prediction, export_format, export_dir, **kwargs)'
    if ($src -notmatch [regex]::Escape($call)) {
        throw 'api.py 里找不到 export(...) 调用，上游结构变了，需人工处理'
    }
    $patched = @"
# PATCH(illust3d): 懒加载，见 sidecar/vendor/PATCHES.md
        from depth_anything_3.utils.export import export
        $call
"@
    $src = $src.Replace($call, $patched.TrimEnd())

    # 注意：Windows PowerShell 5.1 的 Set-Content -Encoding utf8 会写 BOM，
    # 而 BOM 会把 Python 源码的第一行注释弄坏，所以这里用 .NET 无 BOM 写入。
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($apiPy, $src, $utf8NoBom)
    Ok "已打补丁（原文件备份为 api.py.orig）"
}

$patchesMd = Join-Path $RepoRoot 'sidecar\vendor\PATCHES.md'
Set-Content -Path $patchesMd -Value @"
# vendor 补丁记录

## 1. `src/depth_anything_3/api.py` —— export 改懒加载

**为什么**
上游 `api.py` 顶层写了 `from depth_anything_3.utils.export import export`，
而 `utils/export/__init__.py` 又顶层 import `gs` / `colmap` / `depth_vis` / `feat_vis` / `glb` / `npz`。
结果是：只做深度推理也会被拖入 trimesh / open3d / pycolmap / moviepy / gsplat 一整串依赖。

**怎么改（两处文本替换）**
1. 删除顶层 `from depth_anything_3.utils.export import export`
2. 在调用点 `export(prediction, export_format, export_dir, **kwargs)` 之前插入同一行 import

**升级 DA3 时**
删除 `sidecar/vendor/Depth-Anything-3`，重跑 `scripts/setup-env.ps1`。
若上游结构变化，脚本会因找不到匹配文本而报错停下——**这是有意的**，避免静默打错补丁。
"@ -Encoding utf8
Ok '写 sidecar/vendor/PATCHES.md'

# ── 6. 可选：下载模型 ─────────────────────────────────────────────────────
if ($DownloadModel) {
    Step "下载模型 $DownloadModel -> $HfHome"
    # 关掉 xet：hf-mirror 会把请求重定向到 xet-bridge CDN（us.aws.cdn.hf.co），
    # 实测 hf-xet 通道会卡死在 128 MB 不动；走普通 HTTP 稳定（实测 ~1.6 MB/s）。
    $env:HF_HUB_DISABLE_XET = '1'
    $env:HF_HUB_ENABLE_HF_TRANSFER = '0'
    & $EnvPy -c "from huggingface_hub import snapshot_download; p = snapshot_download('$DownloadModel'); print('  ->', p)"
    if ($LASTEXITCODE -ne 0) { throw '模型下载失败' }
    Ok '模型下载完成'
}

# ── 7. 自检 ───────────────────────────────────────────────────────────────
Step '自检'
& $EnvPy -c @'
import sys, os
print('  sys.prefix      :', sys.prefix)
import torch
print('  torch           :', torch.__version__, '| cuda:', torch.cuda.is_available())
print('  PIP_CACHE_DIR   :', os.environ.get('PIP_CACHE_DIR'))
print('  HF_HOME         :', os.environ.get('HF_HOME'))
print('  PYTHONNOUSERSITE:', os.environ.get('PYTHONNOUSERSITE'))
'@

Push-Location $RepoRoot
try {
    & $EnvPy -c "import sys; sys.path.insert(0, 'sidecar/vendor/Depth-Anything-3/src'); import depth_anything_3; print('  DA3 包导入: ok')" 2>&1
} catch {
    Warn "DA3 包导入失败：$($_.Exception.Message)"
} finally { Pop-Location }

Write-Host ''
Write-Host '完成。使用方法：' -ForegroundColor Green
Write-Host "  conda activate $EnvName"
Write-Host "  $EnvPy -c `"import sys; sys.path.insert(0,'sidecar/vendor/Depth-Anything-3/src'); from depth_anything_3.api import DepthAnything3; print('DA3 import ok')`""
Write-Host ''
Write-Host '注意：DA3 不在 site-packages 里，导入前必须把 sidecar/vendor/Depth-Anything-3/src 加进 sys.path。'
