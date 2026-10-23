// 浏览器这边产出的正文块也照 docs/article-blocks.schema.json（由 api/article_blocks.go 生成）。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { buildClipboardArticleBlocks } from '../public/js/parser.js';

const schema = JSON.parse(readFileSync(new URL('../docs/article-blocks.schema.json', import.meta.url), 'utf8'));

// 只认这份说明书用到的几样 JSON Schema 写法。
function errors(value, rule, path = '$') {
  if (rule.$ref) return errors(value, schema.$defs[rule.$ref.replace('#/$defs/', '')], path);
  if (rule.oneOf) {
    const matched = rule.oneOf.filter((r) => errors(value, r, path).length === 0);
    return matched.length === 1 ? [] : [`${path}：不是说明书里的任何一种块（${JSON.stringify(value).slice(0, 80)}）`];
  }
  if ('const' in rule && value !== rule.const) return [`${path} 应为 ${rule.const}`];
  if (rule.enum && !rule.enum.includes(value)) return [`${path} 只能是 ${rule.enum.join('/')}`];
  const kind = Array.isArray(value) ? 'array' : Number.isInteger(value) ? 'integer' : value === null ? 'null' : typeof value;
  if (rule.type && rule.type !== kind) return [`${path} 应为 ${rule.type}，实际 ${kind}`];
  if (kind === 'array' && rule.items) return value.flatMap((item, i) => errors(item, rule.items, `${path}[${i}]`));
  if (kind !== 'object' || !rule.properties) return [];
  const out = [];
  for (const key of rule.required || []) if (!(key in value)) out.push(`${path} 缺 ${key}`);
  for (const [key, v] of Object.entries(value)) {
    if (!rule.properties[key]) out.push(`${path} 不该有 ${key}`);
    else out.push(...errors(v, rule.properties[key], `${path}.${key}`));
  }
  return out;
}

test('粘贴纯文本产出的块照说明书', () => {
  const { blocks } = buildClipboardArticleBlocks('标题\n作者：Sage\n\n第一段正文。\n第二段正文。');
  assert.ok(blocks.length >= 3);
  assert.deepEqual(errors(blocks, schema), []);
});

test('说明书的校验认得出不合规的块', () => {
  assert.equal(errors([{ k: 'txt', runs: [{ type: 'txt', text: '正文' }] }], schema).length, 0);
  assert.notEqual(errors([{ k: 'txt', src: 'https://example.com/a.jpg' }], schema).length, 0, '段落不该有 src');
  assert.notEqual(errors([{ k: 'para', text: '正文' }], schema).length, 0, '没有 para 这种块');
  assert.notEqual(errors([{ k: 'txt', runs: [{ type: 'bold', text: '正文' }] }], schema).length, 0, '片段 type 只有 txt/link');
});
