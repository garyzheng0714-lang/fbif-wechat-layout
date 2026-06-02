import { ASSET_LIBRARY_DEFAULT } from './asset-library-defaults.js';

const clone = value => JSON.parse(JSON.stringify(value));

export function defaultAssetLibraryData() {
  return clone(ASSET_LIBRARY_DEFAULT);
}

export function assetLibraryFromPreset(preset) {
  const raw = preset && preset.config && preset.config.modules && preset.config.modules.asset_library;
  return raw && typeof raw === 'object' ? clone(raw) : defaultAssetLibraryData();
}
