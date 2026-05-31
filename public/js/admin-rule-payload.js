export function serverRuleConfigDocument(configDoc) {
  const { name, description, ...serverConfig } = configDoc || {};
  return serverConfig;
}
