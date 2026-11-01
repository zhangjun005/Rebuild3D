import { createReadStream, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig, type Connect, type Plugin } from 'vite';

/** 仓库根目录（web/ 的上一级） */
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const TESTDATA_DIR = path.join(REPO_ROOT, 'testdata');

/**
 * 只读地把 `testdata/` 暴露给浏览器（仅 dev / preview）。
 *
 * 为什么不把 sample.ilcl 拷进 `web/public/`：那是 1.7 MB 的合成数据，
 * 拷一份等于仓库里存两份；而且 `testdata/` 的归属是数据线，不该被前端复制。
 * 生产环境由 Go 的 `GET /api/cloud/{id}` 提供同样的 ILC1 字节流（见 docs/selection.md §4.2），
 * 所以这里的路径只是开发期替身。
 */
function testdataPlugin(): Plugin {
  const handler: Connect.NextHandleFunction = (req, res, next) => {
    const url = (req.url ?? '').split('?')[0] ?? '';
    if (!url.startsWith('/__testdata/')) {
      next();
      return;
    }
    const name = path.basename(url);
    // 白名单：只允许 testdata/ 下的普通文件名
    if (!/^[\w.-]+$/.test(name)) {
      next();
      return;
    }
    const file = path.join(TESTDATA_DIR, name);
    let size: number;
    try {
      const st = statSync(file);
      if (!st.isFile()) throw new Error('not a file');
      size = st.size;
    } catch {
      next();
      return;
    }
    res.statusCode = 200;
    res.setHeader('Content-Type', 'application/octet-stream');
    res.setHeader('Content-Length', String(size));
    res.setHeader('Cache-Control', 'no-store');
    createReadStream(file).pipe(res);
  };

  return {
    name: 'illust3d-testdata',
    configureServer(server) {
      server.middlewares.use(handler);
    },
    configurePreviewServer(server) {
      server.middlewares.use(handler);
    },
  };
}

export default defineConfig({
  plugins: [testdataPlugin()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
    strictPort: true,
  },
  build: {
    target: 'es2022',
    chunkSizeWarningLimit: 1200,
    reportCompressedSize: true,
  },
});
