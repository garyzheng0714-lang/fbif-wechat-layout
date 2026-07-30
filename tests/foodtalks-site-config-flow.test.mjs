import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { loadLocalRulePreset, saveLocalRulePreset } from '../public/js/rule-presets.js';

function extractObjectMethod(source, signature) {
  const start = source.indexOf(signature);
  assert.notEqual(start, -1, `${signature} should exist`);
  const signatureEnd = source.indexOf(') {', start);
  assert.notEqual(signatureEnd, -1, `${signature} signature should be complete`);
  const bodyStart = signatureEnd + 2;
  let depth = 0;
  for (let i = bodyStart; i < source.length; i++) {
    if (source[i] === '{') depth++;
    if (source[i] === '}') depth--;
    if (depth === 0) return 'function ' + source.slice(start, i + 1);
  }
  assert.fail(`${signature} should be complete`);
}

test.before(async () => {
  // 集成测试同样走源码 grep —— jsdom 不是项目依赖
});

test('engine.js: setActiveConfig exists and writes to _activeConfig', async () => {
  const src = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /setActiveConfig\s*[=:(]/, 'engine.js 必须导出 setActiveConfig');
  assert.match(src, /_activeConfig/, 'engine.js 必须有 _activeConfig 字段');
});

test('engine.js: all process call sites pass config through the unified renderer', async () => {
  const src = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  const processDocxCalls = [...src.matchAll(/template\.processDocx\s*\([^)]*\)/g)];
  assert.ok(processDocxCalls.length >= 1, `processDocx 调用点至少 1 处，实际 ${processDocxCalls.length}`);
  for (const m of processDocxCalls) {
    assert.match(m[0], /config/, `processDocx 调用 "${m[0]}" 必须含 config`);
  }
  const processMdCalls = [...src.matchAll(/template\.processMd\s*\([^)]*\)/g)];
  for (const m of processMdCalls) {
    assert.match(m[0], /config/, `processMd 调用 "${m[0]}" 必须含 config`);
  }
  const processBlocksCalls = [...src.matchAll(/template\.processBlocks\s*\([^)]*\)/g)];
  for (const m of processBlocksCalls) {
    assert.match(m[0], /config/, `processBlocks 调用 "${m[0]}" 必须含 config`);
  }
});

test('engine.js: 当前文章由统一 session 激活，重排不直接 showPreview', async () => {
  // WHY：底部名片、正文页脚、复制层不能各自维护状态。当前文章必须由一个 session 统一激活，
  // 否则规则晚到时会出现左侧有名片、正文底部没有名片的分裂状态。
  const src = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /let _articleSession = null/, 'engine 应维护当前文章 session');
  assert.match(src, /async function buildArticleSession/, 'engine 应集中构建文章 session');
  assert.match(src, /function activateArticleSession/, 'engine 应集中激活当前文章');
  const rerender = src.slice(src.indexOf('async function _engineRerender'));
  assert.match(rerender, /buildArticleSession\(renderInput, result/, '重排应用本次固定输入重建 session');
  assert.match(
    rerender,
    /activateArticleSession\(session,\s*\{\s*preparedImages:\s*reusableImages\s*\}\)/,
    '重排应通过 session 激活预览并复用当前图片节点'
  );
  assert.doesNotMatch(src, /showBatchList|_batchResults|batch-upload/, '单篇产品不得保留不可达的批量队列');
});

test('engine.js: 上传前等待当前规则加载，避免 FoodTalks 底部区块漏渲', async () => {
  // WHY：用户打开页面后立刻上传时，/api/rules/active 可能还没回来；若不等待，正文会按
  // 无 bottom_blocks 的临时配置生成，左侧有名片但正文底部没有名片。
  const src = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /function\s+waitForActiveRuleConfigReady/, 'engine 必须有规则 ready 等待');
  assert.ok((src.match(/await waitForActiveRuleConfigReady\(/g) || []).length >= 2, '单篇/blocks 两条上传路径都要等待规则 ready');
  const waitFn = src.slice(src.indexOf('async function waitForActiveRuleConfigReady'), src.indexOf('function resolveAuthorCardOverrideForArticle'));
  assert.doesNotMatch(waitFn, /Promise\.race|3000/, '规则加载不得三秒后静默使用旧配置继续');
  assert.match(waitFn, /state !== 'resolved'/, '只有服务端确认的规则才能生成最终排版');
  assert.match(src, /addEventListener\('fbif:rules-ready'/, '规则晚到时必须触发补渲染');
  const rerender = src.slice(src.indexOf('async function _engineRerender'));
  assert.match(rerender, /autofillAuthorCard: !!options\.autofillAuthorCard/, '普通重排不得默认覆盖用户手动替换的作者名片');
  assert.match(src, /_engineRerender\(\{ autofillAuthorCard: true \}\)/, '规则晚到补渲才允许重新自动补作者名片');
});

test('engine.js/templates: 底部区块渲染读取 session override，而不是碰运气读全局', async () => {
  // WHY：作者名片 override 只有进入当前文章 session 后才持久化；页脚构建必须显式使用该 session
  // 的 override，否则新稿第一次渲染会仍按旧全局值输出，正文底部漏名片。
  const engine = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(engine, /bottomBlocksOverride: bottomBlocksState\.override/, 'session 应保存本篇底部 override');
  assert.match(
    engine,
    /loadBaseFooterHtml\(\{\s*isRepost,\s*bottomBlocksOverride: bottomBlocksState\.override,/,
    '构建页脚时应传入本篇 override'
  );

  const series = await readFile(
    new URL('../public/js/templates/foodtalks-series.js', import.meta.url),
    'utf8'
  );
  const fbif = await readFile(
    new URL('../public/js/templates/fbif.js', import.meta.url),
    'utf8'
  );
  assert.match(series, /hasOwnProperty\.call\(opts, 'bottomBlocksOverride'\)/, 'FoodTalks 系列页脚应支持显式 override');
  assert.match(fbif, /hasOwnProperty\.call\(opts, 'bottomBlocksOverride'\)/, '公众号页脚应支持显式 override');
});

test('engine.js: 本篇隐藏底部区域时不得 fallback 到旧 footer.html', async () => {
  // WHY：用户关闭“底部区域”时，空 HTML 是明确的“不要输出底部”，不是“模板没接管”。
  // 否则 engine 会把旧 footer.html 塞回来，预览和复制都违背所见即所得。
  const engine = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  const loadBaseFooter = engine.slice(
    engine.indexOf('async function loadBaseFooterHtml'),
    engine.indexOf('// Module-level flag')
  );
  assert.match(loadBaseFooter, /res && typeof res === 'object'/, '模板返回对象时应视为已接管 footer');
  assert.match(loadBaseFooter, /html:\s*res\.html == null \? '' : String\(res\.html\)/, '对象返回的空 html 应被保留为空字符串');
  assert.doesNotMatch(loadBaseFooter, /const html = res && typeof res === 'object' \? res\.html : res/, '不得把对象 html 抽出后再用非空判断决定 fallback');
  assert.ok(
    loadBaseFooter.indexOf('if (res && typeof res ===') < loadBaseFooter.indexOf('const html = res;'),
    '对象接管分支必须早于字符串兼容分支'
  );
});

test('engine.js: 更多文章刷新必须沿用当前文章的底部可见性状态', async () => {
  // WHY：编辑更多文章只是更新卡片，不能偷偷恢复用户已经隐藏的底部区域。
  const engine = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  const updateMore = engine.slice(
    engine.indexOf('window._updateMoreArticles = async function(cardsOverride)'),
    engine.indexOf('dispatchArticleEvent(bodyOnly')
  );
  assert.match(updateMore, /articleMeta:\s*_articleSession && _articleSession\.articleMeta/, '刷新更多文章应带本篇 articleMeta');
  assert.match(updateMore, /bottomBlocksOverride:\s*_articleSession && _articleSession\.bottomBlocksOverride/, '刷新更多文章应带本篇 bottomBlocksOverride');
  assert.match(updateMore, /footerVisibilityOverride:\s*_articleSession && _articleSession\.footerVisibilityOverride/, '刷新更多文章应带本篇 footerVisibilityOverride');
});

test('engine.js: 读取本地底部覆盖时必须迁移旧结构覆盖', async () => {
  // WHY：旧 bb.override 里的 enabled/order/块级 enabled 不能抢在作者默认前生效。
  const engine = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(engine, /stripReadonlyOverride/, 'engine 应复用底部覆盖迁移逻辑');
  assert.match(engine, /return migrateStoredBottomBlocksOverride\(accountId, raw\)/, '读取本地 bottom override 必须先迁移');
  assert.match(engine, /function\s+frontendEditableBlockIDs/, '迁移必须基于当前 bottom_blocks 的 frontend_editable 块集合');
});

test('engine.js: bottom_blocks 启用时不再被旧 footer_enabled 隐藏', async () => {
  // WHY：FoodTalks 系列的名片/BD/群二维码属于 modules.bottom_blocks，
  // 旧的隐藏 footer_enabled=false 只应用于静态 footer.html，不能把新版底部区块整段关掉。
  const engine = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(engine, /function\s+hasManagedBottomBlocksFooter/, 'engine 应识别托管底部区块');
  assert.match(engine, /normalizeFooterVisibilityOverride\([\s\S]{0,220}_articleSession\.footerVisibilityOverride/, '统一 footer 开关必须读取当前文章的显隐覆盖');
  assert.match(engine, /visibilityOverrideHides\(footerVisibilityOverride\.enabled\)[\s\S]{0,80}return false/, '本文隐藏底部区域时必须优先于 bottom_blocks 默认显示');
  assert.match(
    engine,
    /return hasManagedBottomBlocksFooter\(\) \|\| !!\(footerEl && footerEl\.checked\)/,
    'bottom_blocks 启用时应强制显示页脚区块'
  );
  assert.ok((engine.match(/isFooterEnabledForCurrentArticle\(\)/g) || []).length >= 3, '预览更新/更多文章/复制路径都要用统一 footer 开关');
});

test('engine.js: 恢复会话缺少 articleSession 时，底部开关仍刷新预览', async () => {
  // WHY：旧本地会话可能只恢复预览 HTML 缓存，没有完整 articleSession。
  // 此时左侧父级开关不能只更新内存状态，否则右侧预览会继续显示底部。
  const engine = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  const setFooterVisibility = engine.slice(
    engine.indexOf('async function setFooterVisibilityOverride(next)'),
    engine.indexOf('async function setArticleArtifactOverride(next)')
  );
  assert.match(engine, /function\s+refreshFooterPreviewFromVisibility/, 'engine 应有无 session 时的预览刷新兜底');
  assert.match(setFooterVisibility, /if \(!_articleSession\) return refreshFooterPreviewFromVisibility\(\)/, '无 articleSession 时也必须立刻刷新右侧预览');
});

test('engine.js: 网站策略切换可更新当前文章原创/转载状态并重排', async () => {
  // WHY：左侧“原创/转载”是排版策略，不是纯视觉选中态；切换后必须复用现有 isRepost 链路。
  const engine = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(engine, /async function setArticleStrategy\(isRepost\)/, 'engine 应暴露文章策略切换入口');
  assert.match(engine, /_articleSession\.input\s*=\s*\{\s*\.\.\.\(_articleSession\.input \|\| \{\}\),\s*isRepost:\s*nextIsRepost\s*\}/, '策略切换应更新当前 session input');
  assert.match(engine, /_lastRenderInput\s*=\s*\{\s*\.\.\._lastRenderInput,\s*isRepost:\s*nextIsRepost\s*\}/, '无完整 session 时也应更新 lastRenderInput');
  assert.match(engine, /_isRepost\s*=\s*nextIsRepost/, '策略切换应同步模块级 repost 标记');
  assert.match(engine, /await _engineRerender\(\)/, '策略切换后应重排当前文章');
  assert.match(engine, /setArticleStrategy,/, 'engine API 应导出 setArticleStrategy');
});

test('app.html: 暴露当前规则 ready promise 给上传流程等待', async () => {
  const src = await readFile(
    new URL('../public/app.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /function\s+trackActiveRuleHydration/, 'app 应跟踪 active rule hydration');
  assert.match(src, /window\._activeRuleReadyPromise/, 'app 应暴露 ready promise 给 engine');
  assert.match(src, /fbif:rules-ready/, '规则加载完成后应发 rules-ready 事件');
});

test('fbif.js: 3 process entries accept config param', async () => {
  const src = await readFile(
    new URL('../public/js/templates/fbif.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /processDocx\s*\(\s*\{[^}]*config[^}]*\}/, 'fbif processDocx 签名含 config');
  assert.match(src, /processMd\s*\([^)]*config[^)]*\)/, 'fbif processMd 签名含 config');
  assert.match(src, /processBlocks\s*\([^)]*config[^)]*\)/, 'fbif processBlocks 签名含 config');
});

test('foodtalks-site.js: 3 process entries accept config param', async () => {
  const src = await readFile(
    new URL('../public/js/templates/foodtalks-site.js', import.meta.url),
    'utf8'
  );
  // processDocx: config 在解构签名里
  assert.match(src, /processDocx\s*\(\s*\{[^}]*config[^}]*\}/, 'foodtalks-site processDocx 签名含 config');
  // processMd / processBlocks: config 通过 opts 参数传入，函数体内提取
  assert.match(src, /processMd[\s\S]{0,200}config\s*=/, 'foodtalks-site processMd 须从 opts 提取 config');
  assert.match(src, /processBlocks[\s\S]{0,200}config\s*=/, 'foodtalks-site processBlocks 须从 opts 提取 config');
});

test('site.html: 注入 engine 的 config 并入扁平参数（loadConfig）', async () => {
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /function\s+syncEngineConfig/, 'site.html 应有 syncEngineConfig');
  const fn = src.match(/function\s+syncEngineConfig[\s\S]+?\n\}/);
  assert.ok(fn, '找不到 syncEngineConfig 函数体');
  assert.match(fn[0], /\.\.\.\s*flat\b|\.\.\.\s*loadConfig\(\)/, 'syncEngineConfig 必须 spread loadConfig() 的扁平字段');
  assert.match(fn[0], /serverBehavior\s*=\s*\(window\._activeConfig\s*&&\s*window\._activeConfig\.behavior\)\s*\|\|\s*\{\}/,
    'site.html 必须从 channel-aware applyActiveRuleConfig 取网站原始 behavior');
  assert.match(fn[0], /behavior:\s*serverBehavior/,
    'site.html 必须把 lists 与顶层正则原样传给 foodtalks-site 渲染器');
  assert.match(fn[0], /setActiveConfig/, 'syncEngineConfig 必须调 setActiveConfig');
});

test('site.html: admin applyConfig 改规则后触发 engine 同步（验收线1必要环）', async () => {
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /applyConfig\s*\([\s\S]{0,500}syncEngineConfig/, 'applyConfig 内须调 syncEngineConfig（否则后台改规则不重排）');
});

test('site.html: 服务端网站 preset 落本地时带上 behavior，断网恢复不丢自定义规则', async () => {
  const src = await readFile(new URL('../public/site.html', import.meta.url), 'utf8');
  const methodSource = extractObjectMethod(src, 'applyConfig(config, preset, meta = {})');
  const storage = new Map();
  const windowRef = {
    localStorage: {
      getItem(key) { return storage.has(key) ? storage.get(key) : null; },
      setItem(key, value) { storage.set(key, String(value)); },
    },
  };
  globalThis.window = windowRef;
  const deps = {
    adminApplyGeneration: 0,
    window: windowRef,
    ruleSelector: { setSource() {} },
    cacheServerRuleFallback(config, preset) {
      saveLocalRulePreset({ ...(preset || {}), config }, 'ft-default');
    },
    saveConfig: config => config,
    syncEngineConfig: () => ({ status: 'noop' }),
  };
  const applyConfig = new Function('deps', `with (deps) { ${methodSource}; return applyConfig; }`)(deps);
  const behavior = {
    author_regex: '^署名[：:]',
    lists: { wechat_trailer_markers: ['网站自定义尾标'] },
  };
  const preset = {
    id: 'website-custom',
    name: '网站自定义',
    baseline_id: 'ft-default-v1',
    rule_family: 'foodtalks-site',
    behavior,
  };
  try {
    await Promise.resolve(applyConfig({ tip_title_color: '#123456' }, preset, { source: 'server' }));
    const restored = loadLocalRulePreset('ft-default');
    assert.equal(restored.id, 'website-custom');
    assert.equal(restored.config.tip_title_color, '#123456');
    assert.deepEqual(restored.behavior, behavior);
  } finally {
    delete globalThis.window;
  }
});

test('site.html: 页脚侧栏从服务端 segment_order/presets 动态渲染并支持拖拽排序', async () => {
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  const stateSrc = await readFile(
    new URL('../public/js/site-footer-state.js', import.meta.url),
    'utf8'
  );
  assert.match(
    src,
    /data-site-section="footer-trailer"[\s\S]{0,140}id="footerLib"[\s\S]{0,20}open/,
    '底部区域控制区应作为固定“文章底部拖尾”分区默认展开'
  );
  assert.match(src, /serverFooterLibrary\(\)/, '页脚侧栏应读取服务端页脚库');
  assert.match(stateSrc, /footerLib\.presets/, '预设按钮应由服务端 presets 渲染');
  assert.match(stateSrc, /footerLib\.segment_order/, '片段顺序应由服务端 segment_order 渲染');
  assert.match(src, /saveFooterPanelState\(/, '当前文章排序应保存到单个 bottom_blocks 形状状态');
  assert.match(src, /data-seg-row/, '片段行应有拖拽目标');
  assert.match(src, /data-footer-drag-handle/, '片段行应有专用拖拽 handle');
  assert.match(src, /function\s+hydrateFooterDragHandles\(\)/, '拖拽 handle 图标应由 createIcon 注入');
  assert.match(src, /dragstart/, '应支持拖拽排序');
  assert.match(src, /function\s+isEmptyFooterPreset/, '无页脚 preset 应被识别为空页脚状态');
  assert.match(src, /enabled\s*=\s*!\s*isEmptyFooterPreset\(nextSegments\)/, '点击无页脚应同步关闭总开关，点击完整预设应重新打开');
  assert.doesNotMatch(src, /<button type="button" class="ft-footer-preset" data-preset="fbif-original">FBIF原创<\/button>/, '不应在 HTML 静态写死预设按钮');
});

test('site.html: 本篇底部区域开关必须同步到正文流 tip', async () => {
  // WHY：网站 tip 在 foodtalks-site.js 里走正文流，不在底部 HTML 拼接里。
  // 如果前台只关 footer.enabled，tip 会继续显示，违背所见即所得。
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /const\s+effectiveFooterSegments\s*=\s*footerEnabled\s*\?\s*footerActiveSegments\s*:\s*disabledFooterSegments\(footerActiveSegments,\s*footerOrder\)/, '关闭底部区域时必须生成全 false 的本篇片段状态');
  assert.ok(
    src.indexOf('const footerOrder = footerState.order') < src.indexOf('const effectiveFooterSegments ='),
    'effectiveFooterSegments 不能在 footerOrder 声明前读取 footerOrder'
  );
  assert.match(src, /active_preset_id:\s*'__article_override__'/, '传给渲染器的页脚库应使用本篇覆盖 preset');
  assert.match(src, /presets:\s*\[\{\s*id:\s*'__article_override__'[\s\S]{0,120}segments:\s*effectiveFooterSegments/, '本篇覆盖 preset 必须压过服务端 preset，确保 tip 开关即时生效');
  assert.match(src, /active_segments:\s*effectiveFooterSegments/, '旧 active_segments 兼容字段也应同步本篇状态');
});

test('site.html: 关闭底部区域时即使 preset 缺 tip 也要关闭正文流 tip', async () => {
  // WHY：FoodTalks tip 是正文流元素；active segments 缺 tip 时模板默认会继续输出。
  // 父级关闭必须显式补 tip:0，不能只把已有 segment 置 0。
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /function\s+disabledFooterSegments\(segments,\s*order\s*=\s*\[\]\)/, 'disabledFooterSegments 应接收 footer order');
  assert.match(src, /\['tip',\s*\.\.\.order,\s*\.\.\.Object\.keys\(segments \|\| \{\}\)\]/, '关闭态必须强制包含 tip 和当前顺序里的所有片段');
  assert.match(src, /disabledFooterSegments\(footerActiveSegments,\s*footerOrder\)/, '关闭底部区域时必须按当前页脚顺序生成全 false 状态');
});

test('site.html: bottom_blocks 自有块不被旧 fbif_footer 片段缺省误关', async () => {
  // WHY：bottom_blocks 是新主合同；fbif_footer 只是兼容回退。
  // 后台新增的 bottom_blocks-only 区块不应因为旧 preset 没同名 segment 而被映射成 false。
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /function\s+footerSegmentEnabledForBlock\(id,\s*block,\s*effectiveSegments,\s*footerEnabled\)/, 'bottom_blocks 显隐应通过专门桥接函数判定');
  assert.match(src, /Object\.prototype\.hasOwnProperty\.call\(effectiveSegments,\s*id\)/, '只有 legacy segments 显式声明同名 id 时才覆盖 bottom block');
  assert.match(src, /return\s+!\(block && block\.enabled === false\)/, 'legacy segments 缺省时应回到 bottom block 自身默认 enabled');
  assert.match(src, /overrides\[id\]\s*=\s*\{\s*enabled:\s*footerSegmentEnabledForBlock\(id,\s*bbBlocks\[id\],\s*effectiveFooterSegments,\s*footerEnabled\)\s*\}/, 'bottom_blocks override 应从 block 默认和显式本篇开关合成');
});

test('site.html: 服务端页脚结构变化时清理旧本地状态，避免 tip=true 或漏 BD 名片', async () => {
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  const stateSrc = await readFile(
    new URL('../public/js/site-footer-state.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /readFooterPanelState\(/, '本地页脚状态应通过统一 bottom_blocks 状态读取');
  assert.match(stateSrc, /signature:\s*footerPanelSignature/, '本地页脚状态必须带服务端结构签名');
  assert.match(stateSrc, /function\s+normalizeStoredState/, '应集中校验本地状态签名');
  assert.match(stateSrc, /raw\.signature\s*!==\s*defaults\.signature/, '签名变化时应重置到服务端默认');
  assert.match(src, /bd_card:\s*'BD 名片'/, '左侧必须能显示 BD 名片分块');
  assert.match(src, /coop_table:\s*'相关合作表'/, '左侧必须能显示相关合作表分块');
  assert.doesNotMatch(src, /资讯投稿 & 商务合作/, '网站什么是系列不能继续显示旧商务合作标签');
});

test('site.html: 网站什么是系列 BD 名片可按当前文章选择，且新文章重置为规则默认', async () => {
  // WHY：BD 名片是运营手动选择项，不应由 DOCX 解析自动决定；同时不能把本篇选择串到下一篇。
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /let\s+footerBdCardAssetID\s*=\s*''/, 'BD 名片选择应是当前页面内存态，不落入全局默认规则');
  assert.match(src, /FT_FOOTER_BD_CARD_RE/, '候选应限制为网站 ft-series BD 名片素材');
  assert.match(src, /activeRulePack\.id\s*!==\s*'ft-series'/, '选择器必须只在网站 ft-series 生效');
  assert.match(src, /data-bd-card-select/, '页脚侧栏应提供 BD 名片选择控件');
  assert.match(src, /data-bd-card-preview/, 'BD 名片选择应显示当前图片预览');
  assert.match(src, /footerSelectedBdCardAsset/, '预览图应来自当前选择或规则默认素材');
  assert.match(src, /applyFooterArticleOverrides/, '渲染前应把当前文章选择合入 footer library');
  assert.match(src, /replaceBdCardAssetRef/, '选择器只应替换 bd_card 的 asset 引用');
  assert.match(src, /payload\s*===\s*footerLastArticlePayload/, '同一篇文章重排时不应清空手动选择');
  assert.match(src, /footerLastArticlePayload\s*=\s*payload/, '新文章激活时必须记录新的文章 payload');
  assert.match(src, /footerBdCardAssetID\s*=\s*''[\s\S]{0,160}syncEngineConfig\(true\)/, '新文章应清空本篇名片覆盖并回到规则默认');
  assert.match(src, /fbif:article-input-started[\s\S]{0,160}clearFooterArticleOverrides\(false\)/, '新输入开始时应先清空本篇名片覆盖');
});

test('site.html: /ft 底部图片替换通过中性 asset-picker 写入 bottom block fields', async () => {
  // WHY: 网站渠道不能 import 公众号渲染器，也不能硬编码某几个 footer 图片块。
  // 选图入口应动态检测 rich_html 里的 asset token，并把本篇替换写入 _bottomBlocksOverride.fields。
  const src = await readFile(
    new URL('../public/site.html', import.meta.url),
    'utf8'
  );
  assert.match(src, /openAssetPicker[\s\S]{0,120}resolveAssetThumb/, 'site.html 应 import 中性 asset-picker 的打开弹层和缩略图解析');
  assert.match(src, /openAssetPicker\(\{[\s\S]{0,220}scope:\s*'ft-default'/, '网站页脚选图 scope 必须固定 ft-default');
  assert.match(src, /function\s+footerSegmentAssetToken/, '页脚图片块应从 html_lines 动态检测 asset token');
  assert.match(src, /\{\{asset(?:#|:)[^}]+\}\}/, '动态检测应覆盖 asset#id 和 asset:usage 两类 token');
  assert.match(src, /footerSegmentFields/, '前台应维护本篇 footer field overrides');
  assert.match(src, /fields:\s*footerSegmentFields\[id\]/, 'syncEngineConfig 合成 _bottomBlocksOverride 时必须把 fields 合入 block override');
  assert.match(src, /saveFooterPanelState\(\{[\s\S]{0,220}fields:\s*footerSegmentFields/, '持久化应保存本篇 fields');
  assert.match(src, /sameFooterHtmlLines/, '选回规则默认素材时应识别为同默认，避免落无意义 field diff');
  assert.match(src, /delete\s+footerSegmentFields\[key\][\s\S]{0,160}else[\s\S]{0,120}footerSegmentFields\[key\]/, '选回默认应删除 field，只有非默认替换才写入 fields');
  assert.match(src, /footerSegmentFields\s*=\s*\{\}[\s\S]{0,120}saveFooterArticleOverrideState\(\)/, '新文章开始时应清空并保存 footer fields，避免上一稿替换串稿');
  assert.match(src, /saveFooterArticleOverrideState\s*=\s*\(\)\s*=>\s*\{[\s\S]{0,220}saveFooterPanelState/, '清空本篇 fields 的保存回调应写回统一 footer state');
});

test('engine.js: 新文件和 URL blocks 导入都会通知站点清理当前文章覆盖', async () => {
  // WHY：BD 名片选择是本篇文章覆盖；新文件/URL 文章开始处理时必须清空，避免串稿。
  const src = await readFile(
    new URL('../public/js/engine.js', import.meta.url),
    'utf8'
  );
  assert.match(src, /dispatchArticleEvent\('fbif:article-input-started',\s*\{\s*source:\s*'file'/, '文件上传路径应通知新输入开始');
  assert.match(src, /dispatchArticleEvent\('fbif:article-input-started',\s*\{\s*source:\s*'blocks'/, 'URL blocks 路径应通知新输入开始');
});
