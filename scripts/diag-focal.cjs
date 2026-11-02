// 验证「焦距现在真的看得出来」：走真实 UI（改滑块 → 点重新生成），不是直接喂 ILC1。
// 因为焦距的视觉差异依赖「重新生成时保留相机距离」这条 UI 逻辑。
const PW_PATH = 'D:\\AITools\\mcps\\playwright\\node_modules\\playwright';
const path = require('path');
const { chromium } = require(PW_PATH);

const URL = process.argv[2] || 'http://127.0.0.1:5173/';
const IMG = process.argv[3] || 'testdata/illustrations/平涂131905131_p0.png';

async function setFocal(page, value) {
  return page.evaluate((v) => {
    const labels = [...document.querySelectorAll('label')];
    const lb = labels.find((e) => (e.textContent || '').includes('焦距系数'));
    if (!lb) return { error: '找不到焦距系数标签' };
    const input = lb.parentElement?.querySelector('input[type=range]') || lb.querySelector('input[type=range]');
    if (!input) return { error: '找不到滑块' };
    input.value = String(v);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    return { ok: true, applied: input.value };
  }, value);
}

async function clickGen(page) {
  return page.evaluate(() => {
    const btns = [...document.querySelectorAll('button')];
    const b = btns.find((e) => /重新生成/.test(e.textContent || ''));
    if (!b) return { error: '找不到重新生成按钮' };
    if (b.disabled) return { error: '按钮被禁用' };
    const label = b.textContent;
    b.click();
    return { ok: true, label };
  });
}

async function snap(page) {
  return page.evaluate(() => {
    const api = globalThis.illust3d;
    const v = api.viewer;
    return {
      points: api.stats().points,
      cameraDistance: +v.camera.position.distanceTo(v.controls.target).toFixed(4),
      target: [
        +v.controls.target.x.toFixed(3),
        +v.controls.target.y.toFixed(3),
        +v.controls.target.z.toFixed(3),
      ],
    };
  });
}

(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--enable-unsafe-swiftshader'] });
  const page = await browser.newPage({ viewport: { width: 1500, height: 900 } });
  page.on('pageerror', (e) => console.log('[pageerror]', e.message));

  await page.goto(URL, { waitUntil: 'load', timeout: 60000 });
  await page.waitForFunction(() => Boolean(globalThis.illust3d), null, { timeout: 45000 });
  await page.waitForTimeout(800);

  console.log('1) 上传');
  await page.locator('input[type=file]').first().setInputFiles(path.resolve(IMG));
  let pts = 0;
  for (let i = 0; i < 40; i++) {
    await page.waitForTimeout(500);
    pts = await page.evaluate(() => globalThis.illust3d.stats().points);
    if (pts > 200000) break;
  }
  await page.waitForTimeout(800);
  const base = await snap(page);
  console.log('   基线:', JSON.stringify(base));
  await page.screenshot({ path: 'out/shots/focal-ui-10.png' });

  for (const f of ['0.1', '3.0']) {
    console.log(`\n2) 设焦距 ${f} → 重新生成`);
    const set = await setFocal(page, f);
    console.log('   设滑块:', JSON.stringify(set));
    await page.waitForTimeout(300);
    const btn = await clickGen(page);
    console.log('   点按钮:', JSON.stringify(btn));
    // ⚠ 不能等"点数 > 20 万"——点数本来就是 100 万，会立刻退出，截图抢在重新生成之前。
    // 改为：等状态栏从「生成中/推理中」变回「✓ 点云就绪」，再补一点余量。
    let sawBusy = false;
    for (let i = 0; i < 40; i++) {
      await page.waitForTimeout(250);
      const t = await page.evaluate(() => {
        const el = document.querySelector('.input-status');
        return el ? el.textContent || '' : '';
      });
      if (/推理中|生成中|重新推理|正在/.test(t)) sawBusy = true;
      if (sawBusy && /点云就绪/.test(t)) break;
    }
    await page.waitForTimeout(1500);
    const after = await snap(page);
    console.log('   结果:', JSON.stringify(after));
    console.log(`   相机距离变化: ${base.cameraDistance} → ${after.cameraDistance} ${Math.abs(after.cameraDistance - base.cameraDistance) < 0.01 ? '✅ 保留' : '❌ 被重新取景'}`);
    await page.screenshot({ path: `out/shots/focal-ui-${f.replace('.', '')}.png` });
  }

  await browser.close();
})().catch((e) => {
  console.error('FAIL:', e.message);
  process.exit(1);
});
