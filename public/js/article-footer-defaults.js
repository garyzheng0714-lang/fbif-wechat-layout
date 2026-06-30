// article-footer-defaults.js — article metadata driven defaults for bottom_blocks.
//
// This layer sits above block-merge.js. It can derive an article-specific
// default visibility profile, then delegates the final two-layer merge to
// resolveBlocks without changing that neutral contract.

import { resolveBlocks, stripReadonlyOverride } from './blocks/block-merge.js';
import { normalizeAuthorName } from './rule-behavior.js';

function isObj(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function clonePlain(value) {
  if (!isObj(value) && !Array.isArray(value)) return value;
  try { return JSON.parse(JSON.stringify(value)); } catch { return value; }
}

function cleanId(value) {
  return String(value || '').trim();
}

function articleAuthorNormalized(articleMeta) {
  const meta = isObj(articleMeta) ? articleMeta : {};
  const explicit = cleanId(meta.author_normalized);
  if (explicit) return explicit;
  return normalizeAuthorName(meta.author || '');
}

function aliasGroupMatches(groupId, articleMeta, footerDefaults) {
  const group = cleanId(groupId);
  if (!group) return false;
  const aliases = isObj(footerDefaults && footerDefaults.author_aliases)
    ? footerDefaults.author_aliases[group]
    : null;
  if (!Array.isArray(aliases)) return false;
  const actual = articleAuthorNormalized(articleMeta);
  if (!actual) return false;
  return aliases.some(alias => {
    const expected = normalizeAuthorName(alias);
    if (!expected) return false;
    return actual === expected || actual.includes(expected);
  });
}

function profileMatches(profile, articleMeta, footerDefaults) {
  const match = isObj(profile && profile.match) ? profile.match : {};
  if ('author_alias_group' in match) {
    return aliasGroupMatches(match.author_alias_group, articleMeta, footerDefaults);
  }
  return false;
}

function firstMatchingProfile(articleMeta, footerDefaults) {
  const defaults = isObj(footerDefaults) ? footerDefaults : {};
  if (defaults.enabled === false) return null;
  const profiles = Array.isArray(defaults.profiles) ? defaults.profiles : [];
  for (const profile of profiles) {
    if (!isObj(profile)) continue;
    if (profileMatches(profile, articleMeta, defaults)) {
      return {
        id: cleanId(profile.id),
        name: cleanId(profile.name),
        blockVisibility: isObj(profile.block_visibility) ? profile.block_visibility : {},
        artifactVisibility: cleanArticleArtifactVisibility(profile.artifact_visibility),
      };
    }
  }
  return null;
}

export function cleanArticleArtifactVisibility(value) {
  if (!isObj(value)) return {};
  const out = {};
  for (const rawID of Object.keys(value)) {
    const id = cleanId(rawID);
    if (!id) continue;
    const v = value[rawID];
    if (v === true || v === false || v === 'show' || v === 'hide' || v === 'hidden') {
      out[id] = v;
    } else if (isObj(v)) {
      const next = {};
      if (v.hidden === true || v.hidden === false) next.hidden = v.hidden;
      if (v.enabled === true || v.enabled === false) next.enabled = v.enabled;
      if (Object.keys(next).length) out[id] = next;
    }
  }
  return out;
}

export function mergeArticleArtifactOverrides(defaults, override) {
  const out = cleanArticleArtifactVisibility(defaults);
  const extra = cleanArticleArtifactVisibility(override);
  for (const id of Object.keys(extra)) out[id] = clonePlain(extra[id]);
  return Object.keys(out).length ? out : undefined;
}

export function resolveArticleArtifactDefaultsForArticle({
  articleMeta,
  footerDefaults,
} = {}) {
  const profile = firstMatchingProfile(articleMeta, footerDefaults);
  if (!profile) return undefined;
  return Object.keys(profile.artifactVisibility).length ? profile.artifactVisibility : undefined;
}

function applyVisibilityToBlocks(blocks, visibility) {
  if (!isObj(blocks) || !isObj(visibility)) return blocks;
  const next = { ...blocks };
  for (const id of Object.keys(visibility)) {
    if (!isObj(next[id])) continue;
    if (typeof visibility[id] !== 'boolean') continue;
    next[id] = { ...next[id], enabled: visibility[id] };
  }
  return next;
}

function materializeModuleWithVisibility(module, visibility) {
  const mod = isObj(module) ? clonePlain(module) : {};
  if (!isObj(visibility) || !Object.keys(visibility).length) return mod;

  if (Array.isArray(mod.presets)) {
    mod.presets = mod.presets.map(preset => {
      if (!isObj(preset)) return preset;
      return {
        ...preset,
        blocks: applyVisibilityToBlocks(preset.blocks, visibility),
      };
    });
    return mod;
  }

  if (isObj(mod.blocks)) {
    mod.blocks = applyVisibilityToBlocks(mod.blocks, visibility);
  }
  return mod;
}

function visibilityOverrideToBlockOverride(visibilityOverride) {
  const out = {};
  if (isObj(visibilityOverride)) {
    if (visibilityOverride.enabled === true || visibilityOverride.enabled === 'show') out.enabled = true;
    if (visibilityOverride.enabled === false || visibilityOverride.enabled === 'hide') out.enabled = false;
  }
  const src = isObj(visibilityOverride) && isObj(visibilityOverride.blocks)
    ? visibilityOverride.blocks
    : {};
  const overrides = {};
  for (const id of Object.keys(src)) {
    const value = src[id];
    let enabled = null;
    if (value === true || value === 'show') enabled = true;
    if (value === false || value === 'hide') enabled = false;
    if (enabled == null) continue;
    overrides[id] = { enabled };
  }
  if (Object.keys(overrides).length) out.overrides = overrides;
  return out;
}

function mergeBlockOverrides(primary, secondary) {
  const out = isObj(primary) ? clonePlain(primary) : {};
  const extra = isObj(secondary) ? secondary : {};
  if (typeof extra.enabled === 'boolean') out.enabled = extra.enabled;
  if (Array.isArray(extra.order)) out.order = [...extra.order];
  if (isObj(extra.overrides)) {
    if (!isObj(out.overrides)) out.overrides = {};
    for (const id of Object.keys(extra.overrides)) {
      const existing = isObj(out.overrides[id]) ? out.overrides[id] : {};
      out.overrides[id] = { ...existing, ...extra.overrides[id] };
      if (isObj(existing.fields) || isObj(extra.overrides[id].fields)) {
        out.overrides[id].fields = {
          ...(isObj(existing.fields) ? existing.fields : {}),
          ...(isObj(extra.overrides[id].fields) ? extra.overrides[id].fields : {}),
        };
      }
    }
  }
  return out;
}

function frontendEditableBlockIDs(module) {
  const ids = new Set();
  function collect(blocks) {
    if (!isObj(blocks)) return;
    for (const id of Object.keys(blocks)) {
      const block = blocks[id];
      if (isObj(block) && block.frontend_editable === true) ids.add(id);
    }
  }
  if (Array.isArray(module && module.presets)) {
    for (const preset of module.presets) collect(preset && preset.blocks);
  } else {
    collect(module && module.blocks);
  }
  return ids;
}

export function prepareBottomBlocksForArticle({
  module,
  articleMeta,
  footerDefaults,
  contentOverride,
  visibilityOverride,
} = {}) {
  const profile = firstMatchingProfile(articleMeta, footerDefaults);
  const effectiveModule = profile
    ? materializeModuleWithVisibility(module, profile.blockVisibility)
    : (isObj(module) ? module : {});
  const cleanContentOverride = stripReadonlyOverride(
    contentOverride,
    frontendEditableBlockIDs(effectiveModule)
  );
  const effectiveOverride = mergeBlockOverrides(
    cleanContentOverride,
    visibilityOverrideToBlockOverride(visibilityOverride)
  );
  return {
    module: effectiveModule,
    override: effectiveOverride,
    articleFooterProfile: profile ? {
      id: profile.id,
      name: profile.name,
      artifactVisibility: clonePlain(profile.artifactVisibility),
    } : null,
  };
}

export function resolveBottomBlocksForArticle({
  module,
  presetId,
  articleMeta,
  footerDefaults,
  contentOverride,
  visibilityOverride,
} = {}) {
  const prepared = prepareBottomBlocksForArticle({
    module,
    articleMeta,
    footerDefaults,
    contentOverride,
    visibilityOverride,
  });
  const resolved = resolveBlocks(prepared.module, prepared.override, { presetId });
  return {
    ...resolved,
    articleFooterProfile: prepared.articleFooterProfile,
  };
}
