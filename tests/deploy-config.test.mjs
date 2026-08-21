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

test('versioned env schema validates every checked-in example without exposing values', async () => {
  const schemaURL = new URL('../deploy/config/env-schema.json', import.meta.url);
  const validatorURL = new URL('../deploy/scripts/validate-env.py', import.meta.url);
  const schema = JSON.parse(await readFile(schemaURL, 'utf8'));
  assert.equal(schema.schema_version, 1);

  for (const [group, filename] of [
    ['api_managed', 'api.env.example'],
    ['api_publish', 'publish.env.example'],
    ['render', 'render.env.example'],
  ]) {
    const result = await execFileAsync('python3', [
      fileURLToPath(validatorURL),
      fileURLToPath(schemaURL),
      group,
      fileURLToPath(new URL(`../deploy/config/${filename}`, import.meta.url)),
    ]);
    assert.match(result.stdout, new RegExp(`env validation ok \\[${group}\\]`));
    assert.doesNotMatch(result.stdout, /set-in-secret-store|token|password/i,
      'validator 输出不得包含环境变量值');
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

test('dependency review rejects high-severity introductions only when the API is available', async () => {
  const workflow = await text('.github/workflows/dependency-review.yml');
  assert.match(workflow, /pull_request:/);
  assert.match(workflow, /permissions:\s*\n\s*contents: read/);
  assert.match(workflow, /actions\/dependency-review-action@v4/);
  assert.match(workflow, /fail-on-severity: high/);
  // Private repos without GHAS get HTTP 403 from the compare API. Probe and
  // skip that unsupported case so docs-only PRs do not go red. Do not use
  // warn-only / continue-on-error on the review step; those hide real findings.
  assert.match(workflow, /dependency-graph\/compare/);
  assert.match(workflow, /supported=false/);
  assert.match(workflow, /steps\.support\.outputs\.supported == 'true'/);
  assert.doesNotMatch(workflow, /warn-only:\s*true/);
  assert.doesNotMatch(workflow, /continue-on-error:\s*true/);
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
