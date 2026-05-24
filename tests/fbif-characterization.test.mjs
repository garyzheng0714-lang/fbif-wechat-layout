import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { DOMParser as XmldomParser } from '@xmldom/xmldom';
import JSZip from 'jszip';

const storage = {
  getItem() { return null; },
  setItem() {},
  removeItem() {},
};

class DOMParserShim {
  parseFromString(xml, type) {
    const doc = new XmldomParser().parseFromString(xml, type);
    doc.querySelectorAll = (selector) => Array.from(doc.getElementsByTagName(selector));
    return doc;
  }
}

globalThis.DOMParser = DOMParserShim;
globalThis.JSZip = JSZip;
globalThis.window = {
  __APP_BUILD_INFO: { build: 'test' },
  localStorage: storage,
};
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });
globalThis.URL.createObjectURL = (blob) => 'blob:test/' + (blob && blob.size || 0);

const { parseDocx } = await import('../public/js/parser.js?characterization=' + Date.now());
const template = (await import('../public/js/templates/fbif.js?characterization=' + Date.now())).default;
const { processForCopy } = await import('../public/js/css-inline.js?characterization=' + Date.now());
const { DEFAULT_RULE_CONFIG } = await import('../public/js/rule-presets.js?characterization=' + Date.now());

async function renderTeaPyramidFixture() {
  const buffer = await fs.readFile(new URL('./fixtures/tea-pyramid.docx', import.meta.url));
  const docx = await parseDocx(buffer);
  const imageFiles = Object.keys(docx.imgCache).sort();
  const stableImgCache = Object.fromEntries(
    imageFiles.map(file => [file, 'docx-image://' + file])
  );
  const result = template.processDocx({
    paragraphs: docx.paragraphs,
    imgCache: stableImgCache,
    isRepost: false,
  });
  return {
    html: result.lines.join('\n'),
    meta: {
      paragraphs: docx.paragraphs.length,
      image_files: imageFiles,
      lines: result.lines.length,
      imgN: result.imgN,
      headingN: result.headingN,
      html_bytes: Buffer.byteLength(result.lines.join('\n')),
    },
  };
}

test('FBIF DOCX real article output matches the saved correct-version golden', async () => {
  const expected = await fs.readFile(new URL('./golden/tea-pyramid.current.html', import.meta.url), 'utf8');
  const expectedMeta = JSON.parse(await fs.readFile(new URL('./golden/tea-pyramid.current.meta.json', import.meta.url), 'utf8'));
  const actual = await renderTeaPyramidFixture();

  assert.deepEqual(actual.meta, {
    paragraphs: expectedMeta.paragraphs,
    image_files: expectedMeta.image_files,
    lines: expectedMeta.lines,
    imgN: expectedMeta.imgN,
    headingN: expectedMeta.headingN,
    html_bytes: expectedMeta.html_bytes,
  });
  assert.equal(actual.html + '\n', expected);
});

test('FBIF DOCX real article copy HTML matches the saved correct-version golden', async () => {
  const expected = await fs.readFile(new URL('./golden/tea-pyramid.copy.current.html', import.meta.url), 'utf8');
  const themeCSS = await fs.readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
  const actual = await renderTeaPyramidFixture();
  const copyHtml = processForCopy(actual.html, themeCSS, DEFAULT_RULE_CONFIG);

  assert.equal(copyHtml + '\n', expected);
});
