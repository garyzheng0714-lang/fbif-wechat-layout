import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

import {
  DEFAULT_RULE_CONFIG,
  FIELD_BY_KEY,
  RULE_MODULE_FIELDS,
  listEditableRuleFields,
} from '../public/js/rule-document.js';

test('rule document owns editable rule field catalog and defaults', () => {
  const keys = listEditableRuleFields().map(field => field.key);

  assert.ok(keys.includes('font_size'));
  assert.ok(keys.includes('muted_color'));
  assert.ok(keys.includes('more_articles_enabled'));
  assert.equal(DEFAULT_RULE_CONFIG.font_size, '15');
  assert.equal(DEFAULT_RULE_CONFIG.muted_color, '#888888');
  assert.equal(FIELD_BY_KEY.get('font_size').label, '正文字号');
  assert.ok(RULE_MODULE_FIELDS.get('theme').has('font_size'));
  assert.ok(RULE_MODULE_FIELDS.get('more_articles').has('more_articles_enabled'));
});

test('rule-presets delegates rule facts to rule-document', async () => {
  const source = await readFile(new URL('../public/js/rule-presets.js', import.meta.url), 'utf8');

  assert.match(source, /from ['"]\.\/rule-document\.js['"]/);
  assert.doesNotMatch(source, /export const RULE_SCHEMA\s*=\s*Object\.freeze\(\[/);
});
