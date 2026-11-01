// 独立验证客户端的 ILC1 响应校验层。
// 用 Playwright 拦截 /api/infer，返回各种坏响应，看状态栏是否给出明确报错、且保留原点云。
// 非破坏性：不碰真服务端的管道状态。
const PW_PATH = 'D:\\AITools\\mcps\\playwright\\node_modules\\playwright';
const path = require('path');
const { chromium } = require(PW_PATH);

const URL = process.argv[2] || 'http://127.0.0.1:5173/';
const IMG = process.argv[3] || 'testdata/illustrations/平涂131905131_p0.png';

const CASES = [
  { name: '200 + 空 body', status: 200, body: Buffer.alloc(0) },
  { name: 'magic 损坏', status: 200, body: (() => { const b = Buffer.alloc(20); b.write('XXXX', 0); return b; })() },
  { name: '声明 999 点 / 实际 83 字节', status: 200, body: (() => { const b = Buffer.alloc(83); b.write('ILC1', 0); b.writeUInt32LE(999, 4); return b; })() },
  { name: '500 + JSON 错误', status: 500, body: Buffer.from('{"error":"sidecar 不可用"}') },
];

(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true, args: ['--enable-unsafe-swiftshader'] });
  const page = await browser.newPage({ viewport: { width: 1500, height: 900 } });
  page.on('pageerror', (e) => console.log('[pageerror]', e.message));

  await page.goto(URL, { waitUntil: 'load', timeout: 60000 });
  await page.waitForFunction(() => Boolean(globalThis.illust3d), null, { timeout: 45000 });
  await page.waitForTimeout(1000);

  // 先真实上传一次，建立基线点云
  await page.locator('input[type=file]').first().setInputFiles(path.resolve(IMG));
  let baseline = 0;
  for (let i = 0; i < 20; i++) {
    await page.waitForTimeout(500);
    baseline = await page.evaluate(() => globalThis.illust3d.stats().points);
    if (baseline > 200000) break;
  }
  console.log('基线点云:', baseline, '点\n');

  for (const c of CASES) {
    await page.route('**/api/infer', (route) =>
      route.fulfill({ status: c.status, contentType: 'application/octet-stream', body: c.body }),
    );
    // 触发一次「重新生成」（走上传同一条 infer 路径）
    await page.evaluate(() => {
      const api = globalThis.illust3d;
      // 直接调用内部的重新生成不方便，改为再选一次同一个文件（重置 input.value 后才能重复触发）
      const input = document.querySelector('input[type=file]');
      input.value = '';
    });
    await page.locator('input[type=file]').first().setInputFiles(path.resolve(IMG));
    await page.waitForTimeout(1500);

    const r = await page.evaluate(() => {
      const el = document.querySelector('.input-status');
      return { status: el ? el.textContent : '(无状态元素)', points: globalThis.illust3d.stats().points };
    });
    const kept = r.points === baseline;
    const reported = /0 字节|格式错误|长度不一致|失败|错误|不可用|sidecar/.test(r.status || '');
    console.log(`--- ${c.name} ---`);
    console.log('  状态栏:', JSON.stringify((r.status || '').split('\n')[0]));
    console.log('  点数:', r.points, kept ? '✅ 保留原点云' : '❌ 点云被破坏');
    console.log('  有明确报错:', reported ? '✅' : '❌ 静默！');
    await page.unroute('**/api/infer');
  }

  await page.screenshot({ path: 'out/shots/diag-badresp.png' });
  await browser.close();
})().catch((e) => {
  console.error('FAIL:', e.message);
  process.exit(1);
});
