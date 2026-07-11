// admin-article-footer-defaults.js — narrow visual editor for article footer defaults.

function isObj(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

export function defaultArticleFooterDefaultsData() {
  return {
    schema_version: 1,
    enabled: true,
    author_aliases: { mote: ['mote', '莫特'] },
    profiles: [{
      id: 'mote_author',
      name: 'Mote 作者默认',
      match: { author_alias_group: 'mote' },
      block_visibility: { byline: false, more_articles: true },
      artifact_visibility: { mote_ref_author_card: false },
    }],
  };
}

export function disabledArticleFooterDefaultsData() {
  return {
    schema_version: 1,
    enabled: false,
    author_aliases: {},
    profiles: [],
  };
}

export function articleFooterDefaultsSeedForAccount(accountID) {
  const id = String(accountID || '').trim().toLowerCase();
  return id === 'fbif'
    ? defaultArticleFooterDefaultsData()
    : disabledArticleFooterDefaultsData();
}

export function articleFooterDefaultsFromPreset(preset) {
  const raw = preset && preset.config && preset.config.modules && preset.config.modules.article_footer_defaults;
  if (isObj(raw)) return clone(raw);
  return disabledArticleFooterDefaultsData();
}

export function normalizeArticleFooterDefaultsData(raw) {
  const input = isObj(raw) ? raw : {};
  const base = input.enabled === false ? disabledArticleFooterDefaultsData() : defaultArticleFooterDefaultsData();
  const out = { ...base, ...clone(input) };
  out.schema_version = 1;
  out.enabled = out.enabled !== false;
  if (!isObj(out.author_aliases)) out.author_aliases = {};
  if (!Array.isArray(out.profiles)) out.profiles = [];
  return out;
}

function activePreset(module) {
  const presets = Array.isArray(module && module.presets) ? module.presets : [];
  const wanted = module && module.active_preset_id;
  return presets.find(p => p && p.id === wanted) || presets[0] || {};
}

function blockEntries(module) {
  const preset = activePreset(module || {});
  const blocks = isObj(preset.blocks) ? preset.blocks : {};
  const order = Array.isArray(preset.order) ? preset.order.filter(id => blocks[id]) : Object.keys(blocks);
  return order.map(id => [id, blocks[id]]);
}

function blockLabel(id, block) {
  const display = String(block && block.display_name || '').trim();
  if (display) return display;
  const fields = isObj(block && block.fields) ? block.fields : {};
  const title = String(fields.title || fields.intro_text || '').replace(/^[\s/]+|[\s/]+$/g, '').trim();
  if (title) return title;
  if ((block && block.type) === 'spacer') return '间距';
  if ((block && block.type) === 'more_articles') return '更多文章';
  return id;
}

function uniqueProfileID(module, base = 'author_default') {
  const normalized = isObj(module) ? module : {};
  const used = new Set(Object.keys(isObj(normalized.author_aliases) ? normalized.author_aliases : {}));
  for (const profile of Array.isArray(normalized.profiles) ? normalized.profiles : []) {
    if (profile && profile.id) used.add(String(profile.id));
  }
  let id = base;
  let suffix = 2;
  while (used.has(id)) id = base + '_' + suffix++;
  return id;
}

function profileAliasGroup(profile) {
  return String(profile && profile.match && profile.match.author_alias_group || '').trim();
}

function ensureEditableProfiles(module) {
  const normalized = normalizeArticleFooterDefaultsData(module);
  if (!isObj(normalized.author_aliases)) normalized.author_aliases = {};
  if (!Array.isArray(normalized.profiles)) normalized.profiles = [];
  if (!normalized.profiles.length) {
    normalized.profiles = [{
      id: 'author_default',
      name: '新作者默认',
      match: { author_alias_group: 'author_default' },
      block_visibility: {},
    }];
  }
  normalized.profiles.forEach((profile, index) => {
    if (!profile || typeof profile !== 'object') return;
    if (!profile.id) profile.id = uniqueProfileID(normalized, 'author_default_' + (index + 1));
    if (!isObj(profile.match)) profile.match = {};
    if (!profileAliasGroup(profile)) profile.match.author_alias_group = profile.id;
    if (!Array.isArray(normalized.author_aliases[profile.match.author_alias_group])) {
      normalized.author_aliases[profile.match.author_alias_group] = [];
    }
    if (!isObj(profile.block_visibility)) profile.block_visibility = {};
  });
  return normalized;
}

function selectedProfileState(module, selectedID) {
  const normalized = ensureEditableProfiles(module);
  const profiles = normalized.profiles;
  let profile = profiles.find(p => p && p.id === selectedID) || profiles[0];
  if (!profile) {
    const id = uniqueProfileID(normalized);
    profile = { id, name: '新作者默认', match: { author_alias_group: id }, block_visibility: {} };
    profiles.push(profile);
    normalized.author_aliases[id] = [];
  }
  const groupID = profileAliasGroup(profile) || profile.id;
  if (!isObj(profile.match)) profile.match = {};
  profile.match.author_alias_group = groupID;
  if (!Array.isArray(normalized.author_aliases[groupID])) normalized.author_aliases[groupID] = [];
  if (!isObj(profile.block_visibility)) profile.block_visibility = {};
  return { normalized, profile, groupID };
}

function aliasText(module, groupID) {
  const aliases = isObj(module.author_aliases) && Array.isArray(module.author_aliases[groupID])
    ? module.author_aliases[groupID]
    : [];
  return aliases.join('，');
}

function parseAliases(value) {
  return String(value || '')
    .split(/[\n,，]+/)
    .map(s => s.trim())
    .filter(Boolean)
    .slice(0, 20);
}

export function bindArticleFooterDefaultsEditor({
  overlay,
  getModule,
  setModule,
  getBottomBlocks,
}) {
  const mount = overlay && overlay.querySelector('[data-role="article-footer-defaults-editor"]');
  if (!mount) return;
  let selectedID = '';

  function update(mutator, rerender = false) {
    const state = selectedProfileState(getModule ? getModule() : null, selectedID);
    selectedID = state.profile.id;
    mutator(state.normalized, state.profile, state.groupID);
    setModule(state.normalized);
    if (rerender) render();
  }

  function render() {
    const state = selectedProfileState(getModule ? getModule() : null, selectedID);
    selectedID = state.profile.id;
    const { normalized, profile, groupID } = state;
    const blocks = blockEntries(getBottomBlocks ? getBottomBlocks() : {});
    const visibility = isObj(profile.block_visibility) ? profile.block_visibility : {};
    mount.innerHTML = `
      <div class="admin-afd">
        <label class="admin-afd-toggle">
          <input type="checkbox" data-afd-enabled ${normalized.enabled ? 'checked' : ''}>
          <span>启用文章默认方案</span>
        </label>
        <div class="admin-afd-row">
          <label class="admin-afd-field">
            <span>方案</span>
            <select data-afd-profile>
              ${normalized.profiles.map(p => `<option value="${escapeAttr(p.id)}" ${p.id === profile.id ? 'selected' : ''}>${escapeHTML(p.name || p.id)}</option>`).join('')}
            </select>
          </label>
          <button type="button" class="admin-afd-mini" data-afd-add-profile>新增方案</button>
        </div>
        <label class="admin-afd-field">
          <span>方案名称</span>
          <input type="text" data-afd-name value="${escapeAttr(profile.name || '')}" placeholder="例如：作者 A 默认">
        </label>
        <label class="admin-afd-field">
          <span>作者匹配</span>
          <input type="text" data-afd-aliases value="${escapeAttr(aliasText(normalized, groupID))}" placeholder="用逗号分隔；支持短别名包含匹配">
        </label>
        <p class="admin-afd-hint">作者命中任一名称时，默认应用下面的底部模块显隐；前台仍可按单篇临时开启或隐藏。</p>
        <div class="admin-afd-blocks">
          ${blocks.map(([id, block]) => `
            <label>
              <input type="checkbox" data-afd-block="${escapeAttr(id)}" ${visibility[id] !== false ? 'checked' : ''}>
              <span>${escapeHTML(blockLabel(id, block))}</span>
            </label>
          `).join('')}
        </div>
      </div>`;

    const enabled = mount.querySelector('[data-afd-enabled]');
    if (enabled) enabled.addEventListener('change', () => {
      update(next => { next.enabled = enabled.checked; });
    });
    const select = mount.querySelector('[data-afd-profile]');
    if (select) select.addEventListener('change', () => {
      selectedID = select.value;
      render();
    });
    const add = mount.querySelector('[data-afd-add-profile]');
    if (add) add.addEventListener('click', () => {
      update(next => {
        const id = uniqueProfileID(next);
        next.author_aliases[id] = [];
        next.profiles.push({
          id,
          name: '新作者默认',
          match: { author_alias_group: id },
          block_visibility: {},
        });
        selectedID = id;
      }, true);
    });
    const name = mount.querySelector('[data-afd-name]');
    if (name) name.addEventListener('input', () => {
      update((_next, profile) => { profile.name = name.value; });
    });
    const aliases = mount.querySelector('[data-afd-aliases]');
    if (aliases) aliases.addEventListener('input', () => {
      update((next, _profile, aliasGroup) => {
        next.author_aliases[aliasGroup] = parseAliases(aliases.value);
      });
    });
    mount.querySelectorAll('[data-afd-block]').forEach(input => {
      input.addEventListener('change', () => {
        update((_next, profile) => {
          profile.block_visibility[input.dataset.afdBlock] = input.checked;
        });
      });
    });
  }

  render();
}

function escapeHTML(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function escapeAttr(value) {
  return escapeHTML(value).replace(/"/g, '&quot;');
}
