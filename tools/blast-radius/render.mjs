import { readFile, writeFile, mkdir, readdir } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const [root, blocksDir, outDir] = process.argv.slice(2);
const { loadLayoutEngine } = await import(pathToFileURL(path.join(root, 'render-service/layout.mjs')).href);
const engine = await loadLayoutEngine({
  publicDir: path.join(root, 'public'),
  fetchRules: account => readFile(path.join(root, `tests/fixtures/layout-parity/rules--${account}.json`), 'utf8'),
  origin: 'http://127.0.0.1:19191',
  build: 'blast',
});
const accounts = ['fbif', 'wowfood', 'foodtalks-series', 'foodtalks-mp', 'ft-default', 'ft-series'];
const files = (await readdir(blocksDir)).filter(name => name.endsWith('.json'));
let ok = 0;
let fail = 0;
for (const file of files) {
  const payload = JSON.parse(await readFile(path.join(blocksDir, file), 'utf8'));
  const id = file.replace(/\.json$/, '');
  if (!Array.isArray(payload.blocks) || payload.blocks.length === 0) {
    await writeFile(path.join(outDir, `${id}.err.txt`), 'empty blocks\n');
    fail++;
    continue;
  }
  for (const account of accounts) {
    const dest = path.join(outDir, account);
    await mkdir(dest, { recursive: true });
    try {
      const result = await engine.layout({
        account,
        title: payload.title || id,
        repost: true,
        input: {
          kind: 'blocks',
          title: payload.title || id,
          source_name: 'FBIF食品饮料创新',
          blocks: payload.blocks,
        },
      });
      await writeFile(path.join(dest, `${id}.html`), result.html);
      ok++;
    } catch (err) {
      await writeFile(path.join(dest, `${id}.err.txt`), String(err && err.stack || err));
      fail++;
    }
  }
}
console.log(root, 'ok', ok, 'fail', fail);
