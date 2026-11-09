import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const execFileAsync = promisify(execFile);
const root = new URL('../', import.meta.url);

async function text(path) {
  return readFile(new URL(path, root), 'utf8');
}

const validatorPath = fileURLToPath(new URL('../deploy/scripts/validate-env.py', import.meta.url));
const schemaPath = fileURLToPath(new URL('../deploy/config/env-schema.json', import.meta.url));
const configPath = filename => fileURLToPath(new URL(`../deploy/config/${filename}`, import.meta.url));

function envNames(source) {
  return source.split('\n').map(line => line.match(/^([A-Z][A-Z0-9_]*)=/)?.[1]).filter(Boolean);
}

test('versioned env schema validates every checked-in example without exposing values', async () => {
  const schema = JSON.parse(await readFile(schemaPath, 'utf8'));
  assert.equal(schema.schema_version, 1);
  const p0 = await text('.github/workflows/p0-monitoring-contract.yml');

  // 中台下发的变量只在中台文件里：示例和服务器 preflight 一样，叠上中台文件的示例再校验。
  for (const [group, filename, overlay] of [
    ['api_managed', 'api.env.example', 'hub-layout.env.example'],
    ['api_publish', 'publish.env.example', 'hub-layout.env.example'],
    ['render', 'render.env.example', 'hub-render.env.example'],
  ]) {
    const result = await execFileAsync('python3', [
      validatorPath, '--overlay', configPath(overlay), schemaPath, group, configPath(filename),
    ]);
    assert.match(result.stdout, new RegExp(`env validation ok \\[${group}\\]`));
    assert.doesNotMatch(result.stdout, /set-in-secret-store|set-by-fbif-hub|password/i,
      'validator 输出不得包含环境变量值');
    assert.ok(p0.includes(
      `validate-env.py --overlay deploy/config/${overlay} deploy/config/env-schema.json ${group} deploy/config/${filename}`,
    ), `P0 必须叠上 ${overlay} 校验 ${filename}`);
  }
});

// FBIF 中台统一管共用密钥（2026-10-26）：一个变量只有一个出处。中台下发的 10 个（渲染服务 1 个）
// 和统一登录的 6 个 SSO_*（2026-11-10）只出现在中台文件的示例里，CI 生成的 api.env / render.env 和手工 publish.env 都不再写。
test('hub-managed variables live only in the hub file examples', async () => {
  const hubLayout = envNames(await text('deploy/config/hub-layout.env.example'));
  assert.deepEqual([...hubLayout].sort(), [
    'CLOUDCONVERT_API_KEY', 'DISTRIBUTION_API_TOKEN', 'FEISHU_APP_SECRET', 'FEISHU_LOGIN_APP_SECRET',
    'FOODTALKS_TOKEN', 'HUB_KEY', 'PUBLISH_ADMIN_SERVICE_TOKEN', 'PUBLISH_SYNC_SERVICE_TOKEN',
    'RENDER_TOKEN', 'SSO_CENTER_CLIENT_SECRET', 'SSO_CENTER_ISSUER', 'SSO_CLIENT_ID', 'SSO_HUB_KEY',
    'SSO_ISSUER', 'SSO_TOKEN_KEY', 'TIKHUB_API_KEY',
  ]);
  assert.deepEqual(envNames(await text('deploy/config/hub-render.env.example')), ['RENDER_TOKEN']);
  for (const filename of ['api.env.example', 'publish.env.example']) {
    const names = envNames(await text(`deploy/config/${filename}`));
    assert.deepEqual(names.filter(name => hubLayout.includes(name)), [], `${filename} 不得再写中台下发的变量`);
  }
  assert.ok(!envNames(await text('deploy/config/render.env.example')).includes('RENDER_TOKEN'));
  for (const filename of ['hub-layout.env.example', 'hub-render.env.example']) {
    const source = await text(`deploy/config/${filename}`);
    assert.match(source, /hubctl put <编号>/);
    assert.match(source, /FBIF-One-Api 的 docs\/hub\/公共变量和密钥\.md/);
  }
});

test('env validator overlays later files like systemd for required and version checks only', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'fbif-env-overlay-'));
  const secret = 'overlay-secret-must-not-appear';
  const write = async (name, source) => {
    const path = join(directory, name);
    await writeFile(path, source, 'utf8');
    return path;
  };
  const validate = (file, ...overlays) => execFileAsync('python3', [
    validatorPath, ...overlays.flatMap(overlay => ['--overlay', overlay]), schemaPath, 'render', file,
  ]);
  const rejectsWith = (promise, pattern) => assert.rejects(promise, error => {
    assert.match(error.stderr, pattern);
    assert.doesNotMatch(error.stdout + error.stderr, new RegExp(secret), '错误路径不得打印密钥值');
    return true;
  });
  try {
    const renderEnv = configPath('render.env.example');
    // 中台文件：头部注释、同名多行（取最后一行）、别的组的变量都是正常的。
    const hub = await write('render-hub.env', `# 由 FBIF 中台本机代理写\nRENDER_TOKEN=stale\nRENDER_TOKEN=${secret}\nFOODTALKS_TOKEN=${secret}\n`);
    const ok = await validate(renderEnv, hub);
    assert.match(ok.stdout, /env validation ok \[render\]: 7 variables, overlay .*render-hub\.env: 2 variables/);
    assert.doesNotMatch(ok.stdout, new RegExp(secret));

    await rejectsWith(validate(renderEnv), /missing or empty required variables: RENDER_TOKEN/);
    await rejectsWith(validate(renderEnv, join(directory, 'missing.env')), /overlay file not found: .*missing\.env/);

    // 后读的同名为准：叠加文件把值清空、把版本改掉，都按合并后的值判。
    const blank = await write('blank.env', 'RENDER_TOKEN=\n');
    await rejectsWith(validate(renderEnv, hub, blank), /missing or empty required variables: RENDER_TOKEN/);
    await validate(renderEnv, blank, hub);
    const stale = await write('stale-version.env', 'FBIF_CONFIG_SCHEMA_VERSION=2\n');
    await rejectsWith(validate(renderEnv, hub, stale), /config schema version mismatch: FBIF_CONFIG_SCHEMA_VERSION/);

    // 未知变量、重复变量仍只查主文件，叠加文件救不了主文件的错。
    const base = await readFile(renderEnv, 'utf8');
    await rejectsWith(validate(await write('dup.env', `${base}NODE_ENV=production\n`), hub), /duplicate variable: NODE_ENV/);
    await rejectsWith(validate(await write('unknown.env', `${base}FOODTALKS_TOKEN=${secret}\n`), hub), /unknown variables: FOODTALKS_TOKEN/);
    await rejectsWith(validate(renderEnv, await write('broken.env', `not a line ${secret}\n`)), /overlay .*broken\.env: line 1: expected NAME=value/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('env schema covers every runtime environment variable referenced by API and renderer', async () => {
  const schema = JSON.parse(await text('deploy/config/env-schema.json'));
  const declared = new Set(Object.values(schema.groups).flatMap(group => Object.keys(group.variables)));
  const referenced = new Set();

  const apiURL = new URL('../api/', import.meta.url);
  for (const filename of await readdir(apiURL)) {
    if (!filename.endsWith('.go') || filename.endsWith('_test.go')) continue;
    const source = await readFile(new URL(filename, apiURL), 'utf8');
    for (const match of source.matchAll(/os\.(?:Getenv|LookupEnv)\("([A-Z][A-Z0-9_]*)"\)/g)) {
      referenced.add(match[1]);
    }
    for (const match of source.matchAll(/\b\w*Env\w*\s*=\s*"([A-Z][A-Z0-9_]*)"/g)) {
      referenced.add(match[1]);
    }
  }

  for (const filename of ['render-service/server.mjs', 'render-service/render.mjs']) {
    const source = await text(filename);
    for (const match of source.matchAll(/process\.env\.([A-Z][A-Z0-9_]*)/g)) referenced.add(match[1]);
  }
  // 外检看门狗已迁往监控端（CLAUDE.md 规则 30），它的环境变量归那边的 schema 管；
  // 本仓库只声明 API 与渲染服务自己用到的变量。

  assert.deepEqual([...referenced].filter(name => !declared.has(name)).sort(), [],
    '新增运行环境变量时必须同步 schema');
});

test('versioned API and render units pin paths, config version and service hardening', async () => {
  const api = await text('deploy/systemd/fbif-api.service');
  const render = await text('deploy/systemd/fbif-render.service');

  assert.match(api, /^Environment=FBIF_CONFIG_SCHEMA_VERSION=1$/m);
  assert.match(api, /^EnvironmentFile=\/var\/www\/api\/\.env$/m);
  assert.match(api, /^EnvironmentFile=\/var\/www\/api\/publish\.env$/m);
  assert.ok(
    api.indexOf('EnvironmentFile=/var/www/api/publish.env') < api.indexOf('EnvironmentFile=/var/www/api/.env'),
    '部署托管配置必须最后加载，不能再被旧 publish.env 的空上传地址覆盖',
  );
  assert.match(api, /^ExecStart=\/var\/www\/api\/fbif-api -port 9001 -public \/var\/www\/wechat-layout$/m);
  assert.match(api, /^ProtectSystem=strict$/m);
  assert.match(api, /^ReadWritePaths=\/var\/www\/api$/m);

  assert.match(render, /^DynamicUser=yes$/m);
  assert.match(render, /^Environment=FBIF_CONFIG_SCHEMA_VERSION=1$/m);
  assert.match(render, /^EnvironmentFile=\/var\/www\/render-service\.env$/m);
  assert.match(render, /^ExecStart=\/usr\/bin\/node \/var\/www\/render-service\/server\.mjs$/m);
  assert.match(render, /^MemoryMax=512M$/m);
  assert.match(render, /^ProtectSystem=strict$/m);
});

test('env validator fails closed on missing, unknown, duplicate and stale schema values', async () => {
  const validator = await text('deploy/scripts/validate-env.py');
  assert.match(validator, /unknown variables:/);
  assert.match(validator, /duplicate variable:/);
  assert.match(validator, /missing or empty required variables:/);
  assert.match(validator, /config schema version mismatch:/);
  const directory = await mkdtemp(join(tmpdir(), 'fbif-env-validator-'));
  const badEnv = join(directory, 'bad.env');
  const secret = 'must-not-appear-in-errors';
  await writeFile(badEnv, `FBIF_CONFIG_SCHEMA_VERSION=1\nUNKNOWN_VARIABLE=${secret}\n`, 'utf8');
  try {
    await assert.rejects(
      execFileAsync('python3', [
        fileURLToPath(new URL('../deploy/scripts/validate-env.py', import.meta.url)),
        fileURLToPath(new URL('../deploy/config/env-schema.json', import.meta.url)),
        'api_managed',
        badEnv,
      ]),
      error => {
        assert.match(error.stderr, /UNKNOWN_VARIABLE/);
        assert.doesNotMatch(error.stderr, new RegExp(secret), '错误路径不得打印密钥值');
        return true;
      },
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('high-severity vulnerable dependencies are rejected before every deploy', async () => {
  // dependency-review-action 需要 GitHub Advanced Security 依赖图，本私有仓库不支持，
  // 每次都假红（2026-10-08 删除）。漏洞依赖由 deploy 前必跑的 P0 考试拦截。
  const p0 = await text('.github/workflows/p0-monitoring-contract.yml');
  const pkg = JSON.parse(await text('package.json'));
  assert.match(p0, /npm run audit:deps/);
  assert.match(pkg.scripts['audit:deps'], /--audit-level=high/);
  assert.match(p0, /govulncheck@/);
  await assert.rejects(text('.github/workflows/dependency-review.yml'), 'dependency-review 在本仓库只会假红，不得恢复');
});

test('Playwright keeps failure evidence and limits cross-browser work to smoke paths', async () => {
  const config = await text('playwright.config.mjs');
  assert.match(config, /retries: process\.env\.CI \? 1 : 0/);
  assert.match(config, /outputDir: 'test-results\/e2e'/);
  assert.match(config, /screenshot: 'only-on-failure'/);
  assert.match(config, /trace: 'retain-on-failure'/);
  assert.match(config, /video: process\.env\.CI \? 'retain-on-failure' : 'off'/);
  assert.match(config, /name: 'firefox-smoke'/);
  assert.match(config, /name: 'webkit-smoke'/);
  assert.match(config, /grep: smokeTests/g);
  assert.match(config, /E2E_CROSS_BROWSER/);
});
