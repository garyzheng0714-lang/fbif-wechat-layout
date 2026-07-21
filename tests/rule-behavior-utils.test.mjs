// 契约测试：rule-behavior-utils.js —— 行为规则读取/正则编译/文本提取的共享助手。
// 意图（架构体检 S6）：这些函数此前在 rule-behavior-foodtalks.js / rule-behavior-foodtalks-series.js /
// site-contact-layout.js / templates/foodtalks-site.js 各有一份手工同步的拷贝（compileSiteRegex 与
// compileSeriesRegex 曾字节级相同），任何一份漂移都会让「同一份 behavior 配置在不同渠道读出不同值」。
// 本测试锁定唯一实现的语义；若有人改动这里的降级/守卫行为，必须先让这里红。
import test from 'node:test';
import assert from 'node:assert/strict';

const {
  behOf, behTemplate, behParamStr, behList, behaviorRegex,
  behaviorTemplate, behaviorParamStr, behaviorListRaw,
  compileBehaviorRegex, validateBehaviorRegexDocument, validateBehaviorRegexSource,
  MAX_BEHAVIOR_REGEX_INPUT,
  runsToText, elemText, stripSectionPrefix, hasSectionPrefix,
} = await import('../public/js/rule-behavior-utils.js');

test('compileBehaviorRegex: (?i) 内联 flag 转构造函数 flag（服务端 RE2 口径）', () => {
  const re = compileBehaviorRegex('(?i)^abc$');
  assert.equal(re.flags, 'i');
  assert.ok(re.test('ABC'));
});

test('compileBehaviorRegex: RegExp 实例透传不重编译', () => {
  const src = /x/g;
  assert.equal(compileBehaviorRegex(src), src);
});

test('compileBehaviorRegex: 服务端不支持的 flag 不再静默清洗', () => {
  assert.throws(() => compileBehaviorRegex('(?imz)a'), /只支持开头/);
});

test('compileBehaviorRegex: 无内联 flag 时原样编译', () => {
  const re = compileBehaviorRegex('^\\d+$');
  assert.equal(re.flags, '');
  assert.ok(re.test('42'));
});

test('compileBehaviorRegex: 拒绝浏览器回溯爆炸结构，保留现有有界通配符', () => {
  assert.throws(() => validateBehaviorRegexSource('(a+)+$'), /嵌套量词/);
  assert.throws(() => validateBehaviorRegexSource('(a|aa)+$'), /带分支/);
  assert.throws(() => validateBehaviorRegexSource('.*separator.*'), /多个无上界通配符/);
  assert.doesNotThrow(() => validateBehaviorRegexSource('长按.{0,4}识别.{0,2}二维码'));
});

test('compileBehaviorRegex: 超长待匹配文本 fail closed，不进入正则引擎', () => {
  const re = compileBehaviorRegex('^a');
  assert.equal(re.test('a'.repeat(MAX_BEHAVIOR_REGEX_INPUT)), true);
  assert.equal(re.test('a'.repeat(MAX_BEHAVIOR_REGEX_INPUT + 1)), false);
});

test('validateBehaviorRegexDocument: 参数和列表错误返回精确 JSON 路径', () => {
  assert.throws(() => validateBehaviorRegexDocument({
    params: { author_line_regex: { type: 'string', default: '(a+)+$' } },
  }), /behavior\.params\.author_line_regex\.default/);
  assert.throws(() => validateBehaviorRegexDocument({
    lists: { wechat_trailer_markers: ['ok', '.*x.*'] },
  }), /behavior\.lists\.wechat_trailer_markers\[1\]/);
});

test('behParamStr: 读 config.behavior.params.<key>.default；空串/非字符串/缺省一律降级 fallback', () => {
  const config = { behavior: { params: { a: { default: 'v' }, empty: { default: '' }, num: { default: 3 } } } };
  assert.equal(behParamStr(config, 'a', 'fb'), 'v');
  assert.equal(behParamStr(config, 'empty', 'fb'), 'fb'); // 空串不算有效值——降级语义是契约
  assert.equal(behParamStr(config, 'num', 'fb'), 'fb');
  assert.equal(behParamStr(config, 'missing', 'fb'), 'fb');
  assert.equal(behParamStr(null, 'a', 'fb'), 'fb');
});

test('behTemplate: 读 templates.<key>；空串/缺省降级', () => {
  const config = { behavior: { templates: { t: 'hi', e: '' } } };
  assert.equal(behTemplate(config, 't', 'fb'), 'hi');
  assert.equal(behTemplate(config, 'e', 'fb'), 'fb');
  assert.equal(behTemplate({}, 't', 'fb'), 'fb');
});

test('behList: 非空数组才生效，空数组/缺省降级 fallback', () => {
  const config = { behavior: { lists: { l: ['a'], empty: [] } } };
  assert.deepEqual(behList(config, 'l', ['fb']), ['a']);
  assert.deepEqual(behList(config, 'empty', ['fb']), ['fb']);
  assert.deepEqual(behList({}, 'l', ['fb']), ['fb']);
});

test('behaviorRegex: 服务端正则非法时降级编译 fallback 字符串', () => {
  const config = { behavior: { params: { re: { default: '([bad' } } } };
  const re = behaviorRegex(config, 're', '^ok$');
  assert.ok(re.test('ok'));
});

test('behavior 派签名（rule-behavior-foodtalks.js 口径）：直接以 behavior 对象为参', () => {
  const behavior = { templates: { t: 'x' }, params: { p: { default: 'y' } }, lists: { l: ['z'], empty: [] } };
  assert.equal(behaviorTemplate(behavior, 't', 'fb'), 'x');
  assert.equal(behaviorParamStr(behavior, 'p', 'fb'), 'y');
  assert.deepEqual(behaviorListRaw(behavior, 'l'), ['z']);
  assert.equal(behaviorListRaw(behavior, 'empty'), null); // 空列表 → null（调用方自己接 fallback）
  assert.equal(behaviorListRaw(null, 'l'), null);
});

test('behOf: 任意输入都返回对象，绝不 throw', () => {
  assert.deepEqual(behOf(null), {});
  assert.deepEqual(behOf({}), {});
  assert.equal(behOf({ behavior: { a: 1 } }).a, 1);
});

test('runsToText: 只取 txt/link，容忍 null run 与缺 text', () => {
  assert.equal(runsToText([
    { type: 'txt', text: 'a' }, null, { type: 'img', text: 'X' },
    { type: 'link', text: 'b' }, { type: 'txt' },
  ]), 'ab');
  assert.equal(runsToText(null), '');
});

test('elemText: runs 优先，text 字符串兜底，其余空串', () => {
  assert.equal(elemText({ runs: [{ type: 'txt', text: 'r' }], text: 'ignored' }), 'r');
  assert.equal(elemText({ text: 't' }), 't');
  assert.equal(elemText({ text: 42 }), '');
  assert.equal(elemText(null), '');
});

test('stripSectionPrefix/hasSectionPrefix: 中文序号与阿拉伯序号 + 顿号/点', () => {
  assert.equal(stripSectionPrefix('三、标题'), '标题');
  assert.equal(stripSectionPrefix('12．标题'), '标题');
  assert.equal(stripSectionPrefix('无前缀'), '无前缀');
  assert.ok(hasSectionPrefix('一、x'));
  assert.ok(!hasSectionPrefix('x'));
  assert.ok(!hasSectionPrefix(''));
});
