// @kron:intent tooling/diagnostic-discipline
// 诊断脚本：走**真实上传路径**（模拟用户点「选择插画」），不是直接 loadILC1。
// 目的：判断「上传后卡住 / 没有反馈 / 不能转」到底是哪一段的问题。
//
// 用法：node scripts/diag-upload.cjs [url] [图片路径]
const PW_PATH = 'D:\\AITools\\mcps\\playwright\\node_modules\\playwright';
const path = require('path');
const { chromium } = require(PW_PATH);

const URL = process.argv[2] || 'http://127.0.0.1:5173/';
const IMG = process.argv[3] || 'testdata/illustrations/平涂131905131_p0.png';

(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--enable-unsafe-swiftshader'] });
  const page = await browser.newPage({ viewport: { width: 1500, height: 900 } });

  const logs = [];
  page.on('console', (m) => {
    const t = m.type();
    if (t === 'error' || t === 'warning' || m.text().includes('[illust3d]')) {
      logs.push(`[${t}] ${m.text()}`);
    }
  });
  page.on('pageerror', (e) => logs.push(`[pageerror] ${e.message}`));

  await page.goto(URL, { waitUntil: 'load', timeout: 60000 });
  const ok = await page
    .waitForFunction(() => Boolean(globalThis.illust3d), null, { timeout: 45000 })
    .then(() => true)
    .catch(() => false);
  console.log('window.illust3d 就绪:', ok);
  if (!ok) {
    console.log('日志:', logs.join('\n'));
    await browser.close();
    return;
  }

  await page.waitForTimeout(1500);

  // 拦截 fetch，记录所有请求 —— 判断上传路径到底有没有发 /api/infer
  await page.evaluate(() => {
    const w = /** @type {any} */ (globalThis);
    w.__fetchLog = [];
    const orig = w.fetch.bind(w);
    w.fetch = (...args) => {
      const url = typeof args[0] === 'string' ? args[0] : args[0]?.url;
      const t0 = performance.now();
      w.__fetchLog.push({ url, phase: 'start', t: Math.round(t0) });
      return orig(...args).then(
        (r) => {
          w.__fetchLog.push({ url, phase: 'done', status: r.status, t: Math.round(performance.now() - t0) });
          return r;
        },
        (e) => {
          w.__fetchLog.push({ url, phase: 'fail', err: String(e), t: Math.round(performance.now() - t0) });
          throw e;
        },
      );
    };
  });

  const readStatus = () =>
    page.evaluate(() => {
      const el = document.querySelector('.input-status');
      return el ? el.textContent : '(没有 .input-status 元素)';
    });

  console.log('上传前输入区状态:', JSON.stringify(await readStatus()));
  const before = await page.evaluate(() => {
    const api = globalThis.illust3d;
    return { points: api.stats().points, fps: api.stats().fps };
  });
  console.log('上传前:', JSON.stringify(before));

  // 找 file input
  const inputs = await page.locator('input[type=file]').count();
  console.log('页面上的 input[type=file] 数量:', inputs);
  if (inputs === 0) {
    console.log('❌ 找不到文件输入框 —— 这就是"没反应"的原因之一');
    await browser.close();
    return;
  }

  const t0 = Date.now();
  await page.locator('input[type=file]').first().setInputFiles(path.resolve(IMG));
  console.log('已选择文件:', IMG);
  await page.waitForTimeout(400);
  console.log('选择后 0.4s 输入区状态:', JSON.stringify(await readStatus()));

  // 轮询：点数是否变化（从 fixture 118401 变成其它值）
  let changed = false;
  let lastStats = null;
  for (let i = 0; i < 40; i++) {
    await page.waitForTimeout(1000);
    lastStats = await page.evaluate(() => {
      const api = globalThis.illust3d;
      const s = api.stats();
      return { points: s.points, fps: +s.fps.toFixed(1), cameraDistance: +(s.cameraDistance ?? 0).toFixed(3) };
    });
    if (i === 1 || i === 3) console.log(`  ${i + 1}s 状态:`, JSON.stringify(await readStatus()));
    if (lastStats.points !== before.points) {
      changed = true;
      console.log(`  ${i + 1}s: 点数变化 → ${lastStats.points}（耗时 ${((Date.now() - t0) / 1000).toFixed(1)}s）`);
      break;
    }
    if (i % 5 === 4) console.log(`  ${i + 1}s: 仍是 ${lastStats.points} 点...`);
  }

  const fetchLog = await page.evaluate(() => /** @type {any} */ (globalThis).__fetchLog ?? []);
  console.log('');
  console.log('=== fetch 记录 ===');
  if (!fetchLog.length) console.log('  （一次 fetch 都没发生 → onUpload 没被调用）');
  for (const r of fetchLog) console.log(' ', JSON.stringify(r));

  if (!changed) console.log('❌ 30 秒内点数没变 —— 上传/推理路径卡住了');

  // 检查是否有东西挡住画布
  const blocking = await page.evaluate(() => {
    const canvas = document.querySelector('canvas');
    if (!canvas) return { error: 'no canvas' };
    const r = canvas.getBoundingClientRect();
    const cx = r.left + Math.min(r.width / 2, 300);
    const cy = r.top + r.height / 2;
    const el = document.elementFromPoint(cx, cy);
    return {
      canvasRect: [Math.round(r.width), Math.round(r.height)],
      topElementAtCenter: el ? `${el.tagName}.${el.className || ''}` : null,
      isCanvas: el === canvas,
    };
  });
  console.log('画布遮挡检查:', JSON.stringify(blocking));

  // 上传后再试拖拽
  const s0 = await page.evaluate(() => {
    const c = globalThis.illust3d.viewer.camera.position;
    return [+c.x.toFixed(3), +c.y.toFixed(3), +c.z.toFixed(3)];
  });
  const cx = 400, cy = 430;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  for (let i = 1; i <= 8; i++) await page.mouse.move(cx + i * 20, cy + i * 3);
  await page.mouse.up();
  await page.waitForTimeout(600);
  const s1 = await page.evaluate(() => {
    const c = globalThis.illust3d.viewer.camera.position;
    return [+c.x.toFixed(3), +c.y.toFixed(3), +c.z.toFixed(3)];
  });
  const dist = Math.hypot(s0[0] - s1[0], s0[1] - s1[1], s0[2] - s1[2]);
  console.log('上传后拖拽: 相机位移', dist.toFixed(4), dist > 0.05 ? '✅ 能转' : '❌ 不能转');

  await page.screenshot({ path: 'out/shots/diag-upload.png' });
  console.log('截图: out/shots/diag-upload.png');
  console.log('');
  console.log('=== 相关日志 ===');
  console.log(logs.length ? logs.join('\n') : '（无）');

  await browser.close();
})().catch((e) => {
  console.error('FAIL:', e.message);
  process.exit(1);
});
