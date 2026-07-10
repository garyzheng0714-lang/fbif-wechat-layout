export function serverRuleConfigDocument(configDoc) {
  const { name, description, ...serverConfig } = configDoc || {};
  return serverConfig;
}

function clone(value) {
  return value == null ? value : JSON.parse(JSON.stringify(value));
}

// 可视化表单只覆盖公共字段；账号专属顶层字段/结构化模块必须原样往返。
export function preserveUneditableRuleConfig(configDoc, originalConfig) {
  const out = clone(configDoc || {});
  const original = originalConfig && typeof originalConfig === 'object' ? originalConfig : {};
  for (const [key, value] of Object.entries(original)) {
    if (key === 'modules') continue;
    if (!(key in out)) out[key] = clone(value);
  }
  const originalModules = original.modules && typeof original.modules === 'object' ? original.modules : {};
  out.modules = out.modules && typeof out.modules === 'object' ? out.modules : {};
  for (const [key, value] of Object.entries(originalModules)) {
    if (!(key in out.modules)) out.modules[key] = clone(value);
  }
  return out;
}
