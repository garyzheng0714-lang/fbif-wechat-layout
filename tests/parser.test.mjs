import test from 'node:test';
import assert from 'node:assert/strict';
import { esc, escAttr, parseMdRuns, parseMdFrontmatter } from '../public/js/parser.js';

// ---- esc / escAttr ----

test('esc escapes &, <, >', () => {
  assert.equal(esc('a & b < c > d'), 'a &amp; b &lt; c &gt; d');
});

test('esc handles empty string', () => {
  assert.equal(esc(''), '');
});

test('escAttr escapes double quotes in addition to &, <, >', () => {
  assert.equal(escAttr('a="b"&c'), 'a=&quot;b&quot;&amp;c');
});

// ---- parseMdRuns ----

test('parseMdRuns splits bold markers', () => {
  const runs = parseMdRuns('hello **world** end');
  assert.equal(runs.length, 3);
  assert.equal(runs[0].text, 'hello ');
  assert.equal(runs[0].bold, false);
  assert.equal(runs[1].text, 'world');
  assert.equal(runs[1].bold, true);
  assert.equal(runs[2].text, ' end');
  assert.equal(runs[2].bold, false);
});

test('parseMdRuns handles no bold', () => {
  const runs = parseMdRuns('plain text');
  assert.equal(runs.length, 1);
  assert.equal(runs[0].text, 'plain text');
  assert.equal(runs[0].bold, false);
});

test('parseMdRuns handles consecutive bold', () => {
  const runs = parseMdRuns('**a****b**');
  assert.equal(runs.filter(r => r.bold).length, 2);
});

test('parseMdRuns collapses newlines', () => {
  const runs = parseMdRuns('line1\n  line2');
  assert.equal(runs[0].text, 'line1line2');
});

test('parseMdRuns handles empty string', () => {
  const runs = parseMdRuns('');
  assert.equal(runs.length, 0);
});

test('parseMdRuns all bold', () => {
  const runs = parseMdRuns('**everything bold**');
  assert.equal(runs.length, 1);
  assert.equal(runs[0].bold, true);
  assert.equal(runs[0].text, 'everything bold');
});

// ---- parseMdFrontmatter ----

test('parseMdFrontmatter extracts author', () => {
  const { author, content } = parseMdFrontmatter('---\nauthor: "张三"\n---\n正文内容');
  assert.equal(author, '张三');
  assert.equal(content, '正文内容');
});

test('parseMdFrontmatter handles no frontmatter', () => {
  const { author, content } = parseMdFrontmatter('没有 frontmatter 的文本');
  assert.equal(author, '');
  assert.equal(content, '没有 frontmatter 的文本');
});

test('parseMdFrontmatter handles author without quotes', () => {
  const { author, content } = parseMdFrontmatter('---\nauthor: 李四\n---\nbody');
  assert.equal(author, '李四');
  assert.equal(content, 'body');
});

test('parseMdFrontmatter handles frontmatter without author', () => {
  const { author, content } = parseMdFrontmatter('---\ntitle: test\n---\nbody');
  assert.equal(author, '');
  assert.equal(content, 'body');
});

test('parseMdFrontmatter handles empty input', () => {
  const { author, content } = parseMdFrontmatter('');
  assert.equal(author, '');
  assert.equal(content, '');
});
