// 本机截图工具：用系统装的 Microsoft Edge（不用 Playwright MCP，它默认去找 Chrome）。
//
// 用法：
//   node scripts/shot.cjs <url> [输出.png] [宽] [高] [等待ms]
//
// 例：
//   node scripts/shot.cjs http://127.0.0.1:5173/ out/shot.png 1600 900 2500
//
// 会顺带：
//   - 转发页面 console 和 pageerror（方便看 WebGL / 推理报错）
//   - 如果页面暴露了 window.illust3d.stats()，把结果打到 [stats]
const PW_PATH = 'D:\\AITools\\mcps\\playwright\\node_modules\\playwright';
const { chromium } = require(PW_PATH);

(async () => {
  const url = process.argv[2];
  if (!url) {
    console.error('用法: node scripts/shot.cjs <url> [out.png] [w] [h] [waitMs]');
    process.exit(2);
  }
  const out = process.argv[3] || 'out/shot.png';
  const width = parseInt(process.argv[4] || '1600', 10);
  const height = parseInt(process.argv[5] || '900', 10);
  const waitMs = parseInt(process.argv[6] || '2000', 10);

  const browser = await chromium.launch({
    channel: 'msedge',
    headless: true,
    args: ['--enable-unsafe-swiftshader', '--use-angle=default'],
  });
  const page = await browser.newPage({
    viewport: { width, height },
    deviceScaleFactor: 1,
  });

  page.on('console', (m) => {
    const t = m.type();
    if (t === 'error' || t === 'warning') console.log(`[console:${t}]`, m.text());
  });
  page.on('pageerror', (e) => console.log('[pageerror]', e.message));

  await page.goto(url, { waitUntil: 'load', timeout: 60000 });
  await page.waitForTimeout(waitMs);

  await page.screenshot({ path: out });

  try {
    const st = await page.evaluate(() => {
      const g = /** @type {any} */ (globalThis).illust3d;
      if (!g) return null;
      if (typeof g.stats === 'function') return g.stats();
      return { keys: Object.keys(g) };
    });
    if (st) console.log('[stats]', JSON.stringify(st));
  } catch (e) {
    console.log('[stats] 取不到:', e.message);
  }

  await browser.close();
  console.log('saved:', out);
})().catch((e) => {
  console.error('FAIL:', e.message);
  process.exit(1);
});
