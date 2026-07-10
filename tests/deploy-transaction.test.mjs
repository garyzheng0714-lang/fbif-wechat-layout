import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile, readdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const execFileAsync = promisify(execFile);
const root = new URL('../', import.meta.url);

async function projectFile(path) {
  return readFile(new URL(path, root), 'utf8');
}

test('deploy transaction shell is syntactically valid and restores every live surface', async () => {
  const scriptURL = new URL('../deploy/scripts/deploy-transaction.sh', import.meta.url);
  await execFileAsync('bash', ['-n', fileURLToPath(scriptURL)]);
  const script = await readFile(scriptURL, 'utf8');

  for (const surface of ['public', 'render', 'api', 'env', 'caddy']) {
    assert.match(script, new RegExp(`restore_path ${surface}\\b`), `${surface} 必须进入 rollback`);
  }
  assert.match(script, /trap 'on_activate_error \$\?' ERR/);
  assert.match(script, /trap 'on_activate_error 143' TERM/,
    '远端收到 Actions 取消 TERM 时也必须回滚');
  assert.match(script, /caddy validate --adapter caddyfile --config "\$RELEASE_DIR\/caddy\/fbifmp-layout\.caddy"/);
  assert.match(script, /restore_path caddy "\$CADDY_TARGET"/,
    '首次部署没有旧 vhost 时也必须删除失败候选');
  assert.match(script, /cp -a -- "\$BACKUP_ROOT\/\$name" "\$target"/,
    'rollback 重试前必须保留 backup，不能第一次 mv 后失去恢复源');
  assert.match(script, /wait_http_ok http:\/\/127\.0\.0\.1:9001\/api\/health/);
  assert.match(script, /wait_http_ok http:\/\/127\.0\.0\.1:19096\/healthz/);
  assert.match(script, /grep -qx 'DEPLOYMENT_WRITES_PAUSED=1' "\$RELEASE_DIR\/api\.env"/,
    '候选 API 必须以全局写暂停启动');
  assert.match(script, /unpause_release\(\)/);
  assert.match(script, /DEPLOYMENT_WRITES_PAUSED=0/);
  assert.match(script, /调用方必须先完成候选 release 验收/,
    '解除暂停必须明确位于不可逆副作用边界之后');
});

test('workflow preflights before staging, never rsyncs directly into live, and keeps external checks inside rollback window', async () => {
  const workflow = await projectFile('.github/workflows/deploy.yml');
  const deployStepAt = workflow.indexOf('      - name: Deploy transactionally');
  const runMarker = '        run: |\n';
  const runAt = workflow.indexOf(runMarker, deployStepAt);
  const runScript = workflow.slice(runAt + runMarker.length)
    .split('\n')
    .map(line => line.startsWith('          ') ? line.slice(10) : line)
    .join('\n');
  await execFileAsync('bash', ['-n', '-c', runScript]);
  const requiredAt = workflow.indexOf('for required in SSH_KEY HOST USER DEPLOY_PATH ADMIN_PASSWORD RENDER_TOKEN');
  const firstRsyncAt = workflow.indexOf('rsync -avz');
  const activateAt = workflow.indexOf('remote_transaction activate');
  const finalizeAt = workflow.indexOf('remote_transaction finalize');
  const versionCheckAt = workflow.indexOf("'.commit == $sha and .build == $sha'");
  const readinessAt = workflow.indexOf('/api/publish/readiness');
  const acceptAt = workflow.indexOf('release_accepted=1');
  const unpauseAt = workflow.indexOf('remote_transaction unpause');

  assert.ok(requiredAt >= 0 && requiredAt < firstRsyncAt, 'secret preflight 必须早于任何 rsync');
  assert.doesNotMatch(workflow, /\$\{USER\}@\$\{HOST\}:\$\{DEPLOY_PATH\}\//,
    '静态文件不得直接覆盖 live DEPLOY_PATH');
  assert.doesNotMatch(workflow, /\$\{USER\}@\$\{HOST\}:\/var\/www\/render-service\//,
    'render 不得直接覆盖 live 目录');
  assert.match(workflow, /npm ci --omit=dev --no-audit --no-fund/,
    'render 依赖必须从 lockfile 可复现安装');
  assert.match(workflow, /playwright install --with-deps chromium/);
  assert.match(workflow, /npm run test:e2e:ci/,
    '无截图交互 E2E 必须进入部署门禁，不能只留在本机手跑');
  assert.match(workflow, /remote_transaction rollback/);
  assert.match(workflow, /concurrency:\s*\n\s*group: fbifmp-layout-production\s*\n\s*cancel-in-progress: false/,
    '生产部署必须串行排队，不能让两个 run 同时 swap/rollback');
  assert.match(workflow, /trap 'on_deploy_error 143' TERM/,
    '公网验收窗口收到取消信号时也必须请求远端回滚');
  assert.ok(activateAt >= 0 && versionCheckAt > activateAt && finalizeAt > versionCheckAt,
    '公网版本验收必须发生在 activate 后、删除 rollback 备份前');
  assert.ok(readinessAt > versionCheckAt && acceptAt > readinessAt && unpauseAt > acceptAt && finalizeAt > unpauseAt,
    'readiness 必须在暂停态验收；接受 release 后才可解除 worker 暂停并 finalize');
  assert.match(workflow, /DEPLOYMENT_WRITES_PAUSED=1/);
  assert.match(workflow, /\.workers_paused == true/);
  assert.match(workflow, /\.store_valid == true/);
  assert.match(workflow, /\.foodtalks\.target_matches_expected == true/);
  assert.match(workflow, /\.enrich_mode == "dual"/);
  assert.match(workflow, /Release already accepted; refusing automatic rollback/,
    'worker 恢复后不得再自动回滚旧二进制');
  for (const contract of [
    '/api/health',
    '/version.json?deploy=${GITHUB_SHA}',
    '/api/rules/admin/presets',
    '/api/publish/jobs',
    '/api/feishu/card-callback',
    '/api/publish/readiness',
  ]) {
    assert.ok(workflow.includes(contract), `缺少部署验收：${contract}`);
  }
});

test('render service has its own lockfile for staging npm ci', async () => {
  const lock = JSON.parse(await projectFile('render-service/package-lock.json'));
  assert.equal(lock.lockfileVersion, 3);
  assert.equal(lock.packages[''].dependencies.linkedom, '^0.18.12');
  assert.ok(lock.packages['node_modules/linkedom']?.version);
});

test('Caddy whitelist covers every current public API group before static fallback', async () => {
  const vhost = await projectFile('deploy/caddy/fbifmp-layout.caddy');
  const fallbackAt = vhost.indexOf('\n\thandle {');
  assert.ok(fallbackAt > 0, '必须存在最终静态 fallback');
  const routed = vhost.slice(0, fallbackAt);

  for (const route of [
    '/api/oss-upload',
    '/api/wechat-upload',
    '/api/fetch-article',
    '/api/fetch-article-meta',
    '/api/image-proxy',
    '/api/doc-to-docx',
    '/api/doc-cache/*',
    '/api/health',
    '/api/render',
    '/api/html-to-blocks',
    '/api/rules/*',
    '/api/publish/*',
    '/api/feishu/*',
    '/preview/*',
    '/mp/*',
    '/ft/*',
  ]) {
    assert.ok(routed.includes(route), `${route} 必须在静态 fallback 前代理`);
  }

  // 自动从非测试 Go 文件提取公开 API/preview 路由，避免新增 mux handler 时还要靠人
  // 想起同步 Caddy 白名单（/preview 曾因此在线上落成 index.html 空壳）。
  const apiDir = new URL('../api/', import.meta.url);
  const goFiles = (await readdir(apiDir)).filter(name => name.endsWith('.go') && !name.endsWith('_test.go'));
  const goRoutes = new Set();
  for (const name of goFiles) {
    const source = await readFile(new URL(name, apiDir), 'utf8');
    for (const match of source.matchAll(/mux\.HandleFunc\("(?:[A-Z]+ )?(\/(?:api|preview)[^" ]*)"/g)) {
      goRoutes.add(match[1]);
    }
  }

  const caddyPatterns = [];
  for (const line of routed.split('\n')) {
    const pathDirective = line.match(/\bpath\s+(.+)$/);
    if (pathDirective) caddyPatterns.push(...pathDirective[1].trim().split(/\s+/));
    const directHandle = line.match(/^\s*handle\s+(\/[^\s{]+)/);
    if (directHandle) caddyPatterns.push(directHandle[1]);
  }
  const covered = route => caddyPatterns.some(pattern => {
    if (pattern.endsWith('/*')) {
      const base = pattern.slice(0, -2);
      return route === base || route.startsWith(base + '/');
    }
    return route === pattern;
  });
  for (const route of goRoutes) {
    assert.ok(covered(route), `Go 路由 ${route} 未被 Caddy 白名单覆盖`);
  }
});
