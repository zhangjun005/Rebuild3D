// 诊断脚本：验证「输入图片后拖不动」的真实原因。
// 读相机位姿 → 模拟拖拽/滚轮 → 再读位姿 → 看是否变化。
const PW_PATH = 'D:\\AITools\\mcps\\playwright\\node_modules\\playwright';
const fs = require('fs');
const path = require('path');
const { chromium } = require(PW_PATH);

const URL = process.argv[2] || 'http://127.0.0.1:5173/';
const ILCL = process.argv[3] || 'out/res1008-400k.ilcl';

async function snap(page) {
  return page.evaluate(() => {
    const api = /** @type {any} */ (globalThis).illust3d;
    const v = api.viewer;
    const c = v.camera.position;
    return {
      pos: [+c.x.toFixed(4), +c.y.toFixed(4), +c.z.toFixed(4)],
      target: [
        +v.controls.target.x.toFixed(4),
        +v.controls.target.y.toFixed(4),
        +v.controls.target.z.toFixed(4),
      ],
      autoSway: v.params.autoSway,
      parallaxMode: v.params.parallaxMode,
      enableRotate: v.controls.enableRotate,
      enableZoom: v.controls.enableZoom,
      enablePan: v.controls.enablePan,
      controlsEnabled: v.controls.enabled,
      pointSize: +v.params.pointSize.toFixed(3),
    };
  });
}

const d = (a, b) => Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);

(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--enable-unsafe-swiftshader'] });
  const page = await browser.newPage({ viewport: { width: 1400, height: 860 } });
  page.on('pageerror', (e) => console.log('[pageerror]', e.message));
  await page.goto(URL, { waitUntil: 'load', timeout: 60000 });
  await page.waitForFunction(() => Boolean(globalThis.illust3d), null, { timeout: 45000 });

  // 载入真实点云（模拟"输入图片后"的状态）
  const b64 = fs.readFileSync(path.resolve(ILCL)).toString('base64');
  await page.evaluate((data) => {
    const bin = atob(data);
    const buf = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
    globalThis.illust3d.loadILC1(buf.buffer, 'diag');
  }, b64);
  await page.waitForTimeout(1200);

  const s0 = await snap(page);
  console.log('载入后初始状态:', JSON.stringify(s0));

  // 画布中心（面板在右侧，取左侧区域）
  const cx = 500, cy = 430;

  // ① 左键拖拽（OrbitControls 默认 = 旋转）
  await page.mouse.move(cx, cy);
  await page.mouse.down({ button: 'left' });
  for (let i = 1; i <= 10; i++) await page.mouse.move(cx + i * 20, cy + i * 4);
  await page.mouse.up({ button: 'left' });
  await page.waitForTimeout(700);
  const s1 = await snap(page);
  console.log('左键拖拽后:', JSON.stringify({ pos: s1.pos, autoSway: s1.autoSway, enableRotate: s1.enableRotate }));
  console.log('  → 相机位移量:', d(s0.pos, s1.pos).toFixed(4), '（0 = 没动）');

  // ② 关掉自动运动后再拖
  await page.evaluate(() => {
    const v = globalThis.illust3d.viewer;
    v.params.autoSway = false;
    v.applyParams(v.params);
  });
  await page.waitForTimeout(300);
  const s2 = await snap(page);
  await page.mouse.move(cx, cy);
  await page.mouse.down({ button: 'left' });
  for (let i = 1; i <= 10; i++) await page.mouse.move(cx - i * 20, cy - i * 6);
  await page.mouse.up({ button: 'left' });
  await page.waitForTimeout(700);
  const s3 = await snap(page);
  console.log('关掉 autoSway 后拖拽:', JSON.stringify({ pos: s3.pos, enableRotate: s3.enableRotate, parallaxMode: s3.parallaxMode }));
  console.log('  → 相机位移量:', d(s2.pos, s3.pos).toFixed(4));

  // ③ 关掉视差模式再拖（此时 enableRotate 应为 true）
  await page.evaluate(() => {
    const v = globalThis.illust3d.viewer;
    v.params.parallaxMode = false;
    v.applyParams(v.params);
  });
  await page.waitForTimeout(300);
  const s4 = await snap(page);
  await page.mouse.move(cx, cy);
  await page.mouse.down({ button: 'left' });
  for (let i = 1; i <= 10; i++) await page.mouse.move(cx + i * 22, cy + i * 3);
  await page.mouse.up({ button: 'left' });
  await page.waitForTimeout(700);
  const s5 = await snap(page);
  console.log('关掉视差模式后拖拽:', JSON.stringify({ pos: s5.pos, enableRotate: s5.enableRotate }));
  console.log('  → 相机位移量:', d(s4.pos, s5.pos).toFixed(4));

  // ④ 滚轮缩放
  const dist0 = Math.hypot(...s5.pos.map((v, i) => v - s5.target[i]));
  await page.mouse.move(cx, cy);
  await page.mouse.wheel(0, -600);
  await page.waitForTimeout(600);
  const s6 = await snap(page);
  const dist1 = Math.hypot(...s6.pos.map((v, i) => v - s6.target[i]));
  console.log('滚轮缩放:', JSON.stringify({ pos: s6.pos }));
  console.log('  → 相机到 target 距离:', dist0.toFixed(4), '→', dist1.toFixed(4));

  await browser.close();
})().catch((e) => {
  console.error('FAIL:', e.message);
  process.exit(1);
});
