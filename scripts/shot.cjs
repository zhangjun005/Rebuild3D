// 本机截图 + 渲染验证工具：用系统装的 Microsoft Edge。
// （Playwright MCP 默认去找 Chrome，本机没装，所以这里直接 require playwright 并指定 channel=msedge）
//
// 能力：
//   · 加载任意 ILC1 文件直接渲染（不经过 Go），用于 A/B 对比
//   · 切换预设、覆盖参数
//   · 打印 window.illust3d.stats()（fps / 点数 / 取景 / 点大小）
//   · 裁掉右侧面板，只出画面（便于并排比较）
//
// 用法：
//   node scripts/shot.cjs <url> [out.png] [选项]
//
// 选项：
//   --w <px> --h <px>            视口尺寸（默认 1600x900）
//   --wait <ms>                  加载后等待（默认 2000）
//   --ilcl <path>                读取 ILC1 并调 window.illust3d.loadILC1()
//   --preset <key>               切预设（stipple-print / halftone-manga / engraving / ink / raw）
//   --params '<json>'            覆盖参数，如 '{"inkSource":"depth"}'
//                                ⚠ PowerShell 会吃掉 JSON 里的双引号，优先用 --param
//   --param <k=v>                覆盖单个参数（可重复），值自动识别 number/bool/string
//                                PowerShell 里安全，例：--param inkSource=depth --param inkPolarity=dark
//   --clip <x,y,w,h>             只截指定区域
//   --no-panel                   裁掉右侧面板（约 275px 宽）
//   --angle <deg>                绕 target 水平旋转指定角度（会先关掉自动运动）
//
// 例：
//   node scripts/shot.cjs http://127.0.0.1:5173/ out/a.png --ilcl out/ab-ink.ilcl --preset engraving --no-panel
const PW_PATH = 'D:\\AITools\\mcps\\playwright\\node_modules\\playwright';
const fs = require('fs');
const path = require('path');
const { chromium } = require(PW_PATH);

const PANEL_W = 275;

function parseArgs(argv) {
  const o = {
    url: null,
    out: 'out/shot.png',
    w: 1600,
    h: 900,
    wait: 2000,
    ilcl: null,
    preset: null,
    params: null,
    paramList: [],
    clip: null,
    noPanel: false,
    angle: 0,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--w') o.w = parseInt(argv[++i], 10);
    else if (a === '--h') o.h = parseInt(argv[++i], 10);
    else if (a === '--wait') o.wait = parseInt(argv[++i], 10);
    else if (a === '--ilcl') o.ilcl = argv[++i];
    else if (a === '--preset') o.preset = argv[++i];
    else if (a === '--params') o.params = JSON.parse(argv[++i]);
    else if (a === '--param') o.paramList.push(argv[++i]);
    else if (a === '--angle') o.angle = parseFloat(argv[++i]);
    else if (a === '--clip') {
      const [x, y, w, h] = argv[++i].split(',').map(Number);
      o.clip = { x, y, width: w, height: h };
    } else if (a === '--no-panel') o.noPanel = true;
    else if (!o.url) o.url = a;
    else o.out = a;
  }
  if (o.noPanel && !o.clip) {
    o.clip = { x: 0, y: 0, width: o.w - PANEL_W, height: o.h };
  }
  return o;
}

(async () => {
  const o = parseArgs(process.argv.slice(2));
  if (!o.url) {
    console.error('用法: node scripts/shot.cjs <url> [out.png] [--ilcl f] [--preset k] [--params json] [--no-panel]');
    process.exit(2);
  }

  const browser = await chromium.launch({
    channel: 'msedge',
    headless: true,
    args: ['--enable-unsafe-swiftshader', '--use-angle=default'],
  });
  const page = await browser.newPage({ viewport: { width: o.w, height: o.h }, deviceScaleFactor: 1 });

  page.on('console', (m) => {
    const t = m.type();
    if (t === 'error' || t === 'warning') console.log(`[console:${t}]`, m.text());
  });
  page.on('pageerror', (e) => console.log('[pageerror]', e.message));

  await page.goto(o.url, { waitUntil: 'load', timeout: 60000 });

  // 等 window.illust3d 就绪
  const ready = await page
    .waitForFunction(() => Boolean(/** @type {any} */ (globalThis).illust3d), null, { timeout: 45000 })
    .then(() => true)
    .catch(() => false);
  if (!ready) {
    console.log('⚠ window.illust3d 未就绪（可能 Go 服务没起，页面回退到 fixture 模式）');
  }

  // 1) 载入 ILC1
  if (o.ilcl && ready) {
    const p = path.resolve(o.ilcl);
    const b64 = fs.readFileSync(p).toString('base64');
    const n = await page.evaluate(async (data) => {
      const bin = atob(data);
      const buf = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
      const api = /** @type {any} */ (globalThis).illust3d;
      return api.loadILC1(buf.buffer, /** @type {any} */ (globalThis).__label || 'ILC1');
    }, b64);
    console.log(`[ilcl] ${path.basename(p)} → ${n} 点`);
  }

  // 2) 切预设
  if (o.preset && ready) {
    await page.evaluate((key) => {
      const api = /** @type {any} */ (globalThis).illust3d;
      api.viewer.applyPreset(key);
      api.syncPanel();
    }, o.preset);
    console.log('[preset]', o.preset);
  }

  // 3) 覆盖参数（--params JSON 或 --param k=v，后者可重复且对 PowerShell 安全）
  const patch = { ...(o.params || {}) };
  for (const kv of o.paramList) {
    const idx = kv.indexOf('=');
    if (idx < 0) {
      console.log('⚠ --param 格式应为 k=v，忽略:', kv);
      continue;
    }
    const k = kv.slice(0, idx);
    const raw = kv.slice(idx + 1);
    let v;
    if (raw === 'true') v = true;
    else if (raw === 'false') v = false;
    else if (raw !== '' && !Number.isNaN(Number(raw))) v = Number(raw);
    else v = raw;
    patch[k] = v;
  }
  if (Object.keys(patch).length && ready) {
    await page.evaluate((p) => {
      const api = /** @type {any} */ (globalThis).illust3d;
      Object.assign(api.viewer.params, p);
      api.viewer.setParams(api.viewer.params);
      api.syncPanel();
    }, patch);
    console.log('[params]', JSON.stringify(patch));
  }

  await page.waitForTimeout(o.wait);

  // 绕 target 水平旋转：用于检查「层间散落点云」在侧视下的表现。
  // 先关掉自动运动，否则 tick 每帧会用公式覆盖相机位姿。
  if (o.angle && ready) {
    await page.evaluate((deg) => {
      const api = /** @type {any} */ (globalThis).illust3d;
      const v = api.viewer;
      v.params.autoSway = false;
      api.viewer.setParams(api.viewer.params);
      const t = v.controls.target;
      const c = v.camera.position;
      const dx = c.x - t.x;
      const dz = c.z - t.z;
      const r = Math.hypot(dx, dz);
      const a = Math.atan2(dx, dz) + (deg * Math.PI) / 180;
      c.x = t.x + r * Math.sin(a);
      c.z = t.z + r * Math.cos(a);
      v.controls.update();
    }, o.angle);
    await page.waitForTimeout(700);
    console.log('[angle]', o.angle, 'deg');
  }

  if (o.clip) {
    await page.screenshot({ path: o.out, clip: o.clip });
  } else {
    await page.screenshot({ path: o.out });
  }

  try {
    const st = await page.evaluate(() => {
      const api = /** @type {any} */ (globalThis).illust3d;
      return api && typeof api.stats === 'function' ? api.stats() : null;
    });
    if (st) console.log('[stats]', JSON.stringify(st));
  } catch (e) {
    console.log('[stats] 取不到:', e.message);
  }

  await browser.close();
  console.log('saved:', o.out);
})().catch((e) => {
  console.error('FAIL:', e.message);
  process.exit(1);
});
