// 截圖設計稿：node design/tools/shot.mjs Main Posts ...  （H=1500 可改視窗高度）
// 需要 Playwright；PLAYWRIGHT_MODULE 可指定 playwright 的 index.mjs 路徑。
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
const mod = process.env.PLAYWRIGHT_MODULE || 'playwright';
const { chromium } = await import(mod);
const here = path.dirname(fileURLToPath(import.meta.url));
const project = path.resolve(here, '..', 'project');
const out = path.resolve(here, '..', 'shots');
await (await import('node:fs/promises')).mkdir(out, { recursive: true });
const names = process.argv.slice(2);
if (!names.length) { console.error('用法：node design/tools/shot.mjs <板名> [板名…]'); process.exit(1); }
const b = await chromium.launch();
for (const n of names) {
  const p = await b.newPage({ viewport: { width: 1440, height: +(process.env.H || 1000) } });
  await p.goto(pathToFileURL(path.join(project, `${n}.dc.html`)).href);
  await p.waitForTimeout(1200);
  await p.screenshot({ path: path.join(out, `${n}.png`), fullPage: true });
  await p.close();
  console.log('shot', n);
}
await b.close();
