// 端到端验收：上传 → 检查点云 → 导出 PNG → 旋转 → 报告。
// 每次改动后跑这个，防止退化。
//
// 用法：node scripts/accept.cjs [url] [图片]
const PW_PATH = 'D:\\AITools\\mcps\\playwright\\node_modules\\playwright';
const path = require('path');
const { chromium } = require(PW_PATH);

const URL = process.argv[2] || 'http://127.0.0.1:5173/';
const IMG = process.argv[3] || 'testdata/illustrations/平涂131905131_p0.png';

let pass = 0;
let fail = 0;
function check(name, ok, detail) {
  if (ok) pass++;
  else fail++;
  console.log(`  ${ok ? '✅' : '❌'} ${name}${detail ? ' — ' + detail : ''}`);
}

(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--enable-unsafe-swiftshader'] });
  const page = await browser.newPage({ viewport: { width: 1500, height: 900 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));

  console.log('1) 加载页面');
  await page.goto(URL, { waitUntil: 'load', timeout: 60000 });
  const ready = await page
    .waitForFunction(() => Boolean(globalThis.illust3d), null, { timeout: 45000 })
    .then(() => true)
    .catch(() => false);
  check('window.illust3d 就绪', ready);

  console.log('2) 上传插画 → 生成点云');
  const t0 = Date.now();
  await page.locator('input[type=file]').first().setInputFiles(path.resolve(IMG));
  let points = 0;
  for (let i = 0; i < 40; i++) {
    await page.waitForTimeout(500);
    points = await page.evaluate(() => globalThis.illust3d.stats().points);
    if (points > 200000) break;
  }
  const uploadSec = (Date.now() - t0) / 1000;
  check('点数 = 1,000,000（默认档位）', points === 1000000, `实际 ${points} · ${uploadSec.toFixed(1)}s`);
  check('HUD 显示点数', await page.evaluate(() => {
    const hud = document.querySelector('.hud, [class*=hud], #hud');
    return hud ? hud.textContent.includes((1000000).toLocaleString('en-US')) : false;
  }), '(若选择器不匹配则跳过)');

  console.log('3) 导出 PNG');
  const png = await page.evaluate(async () => {
    const api = globalThis.illust3d;
    if (typeof api.exportPng !== 'function') return { error: 'no exportPng' };
    try {
      const blob = await api.exportPng({ width: 1080, height: 1080 });
      return { size: blob.size, type: blob.type };
    } catch (e) {
      return { error: String(e) };
    }
  });
  check('exportPng 返回 PNG blob', !png.error && png.size > 500000, png.error || `${(png.size / 1048576).toFixed(2)} MB ${png.type}`);

  console.log('4) 旋转交互');
  const before = await page.evaluate(() => {
    const c = globalThis.illust3d.viewer.camera.position;
    return [c.x, c.y, c.z];
  });
  await page.mouse.move(400, 430);
  await page.mouse.down();
  for (let i = 1; i <= 8; i++) await page.mouse.move(400 + i * 22, 430 + i * 3);
  await page.mouse.up();
  await page.waitForTimeout(600);
  const after = await page.evaluate(() => {
    const c = globalThis.illust3d.viewer.camera.position;
    return [c.x, c.y, c.z];
  });
  const disp = Math.hypot(before[0] - after[0], before[1] - after[1], before[2] - after[2]);
  check('左键拖拽能旋转', disp > 0.1, `位移 ${disp.toFixed(4)}`);
  const autoSway = await page.evaluate(() => globalThis.illust3d.viewer.params.autoSway);
  check('拖拽后自动运动关闭', autoSway === false);

  console.log('5) 滚轮缩放');
  const d0 = await page.evaluate(() => {
    const v = globalThis.illust3d.viewer;
    return v.camera.position.distanceTo(v.controls.target);
  });
  await page.mouse.wheel(0, -500);
  await page.waitForTimeout(500);
  const d1 = await page.evaluate(() => {
    const v = globalThis.illust3d.viewer;
    return v.camera.position.distanceTo(v.controls.target);
  });
  check('滚轮缩放生效', Math.abs(d1 - d0) > 0.05, `${d0.toFixed(3)} → ${d1.toFixed(3)}`);

  console.log('6) 帧率与错误');
  await page.waitForTimeout(1200);
  const st = await page.evaluate(() => globalThis.illust3d.stats());
  check('帧率 ≥ 60', st.fps >= 60, `${st.fps.toFixed(0)} fps`);
  check('无页面异常', errors.length === 0, errors.slice(0, 2).join('; ') || '无');

  await page.screenshot({ path: 'out/shots/ACCEPT-e2e.png' });
  console.log(`\n截图: out/shots/ACCEPT-e2e.png`);
  console.log(`\n========== 通过 ${pass} / 失败 ${fail} ==========`);
  await browser.close();
  process.exit(fail === 0 ? 0 : 1);
})().catch((e) => {
  console.error('FAIL:', e.message);
  process.exit(1);
});
