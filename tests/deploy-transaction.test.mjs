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
  const watchdogTimer = await projectFile('deploy/monitoring/fbif-monitor-watchdog.timer');

  for (const surface of [
    'public', 'render', 'api', 'env', 'caddy',
    'watchdog', 'watchdog_service', 'watchdog_timer',
    'api_service', 'render_service', 'render_env', 'config_schema',
  ]) {
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
  assert.match(script, /grep -qx 'ENV=production' "\$RELEASE_DIR\/api\.env"/,
    '候选 API 必须显式启用生产启动策略');
  assert.match(script, /grep -qx 'DEPLOYMENT_WRITES_PAUSED=1' "\$RELEASE_DIR\/api\.env"/,
    '候选 API 必须以全局写暂停启动');
  assert.match(script, /RELEASE_DIR=\$\(realpath -m -- "\$RELEASE_DIR"\)/);
  assert.match(script, /PUBLIC_TARGET=\$\(realpath -m -- "\$PUBLIC_TARGET"\)/);
  assert.match(script, /"\$RELEASE_DIR" == "\/var\/www\/\.fbif-layout-releases\/\$DEPLOY_ID"/,
    'root 脚本不得只做字符串前缀校验');
  assert.match(script, /"\$PUBLIC_TARGET" == "\/var\/www\/wechat-layout"/,
    '生产静态目录必须是精确 allowlist');
  assert.match(script, /unpause_release\(\)/);
  assert.match(script, /DEPLOYMENT_WRITES_PAUSED=0/);
  assert.match(script, /prepare_monitoring\(\)/);
  assert.match(script, /start_monitoring\(\)/);
  assert.match(script, /systemd-analyze verify "\$WATCHDOG_SERVICE_TARGET" "\$WATCHDOG_TIMER_TARGET"/,
    '候选 systemd 单元必须在解暂停前验证');
  assert.match(script, /watchdog_timer\.was_enabled/,
    '回滚必须恢复旧 timer 的 enable 状态');
  assert.match(script, /candidate watchdog timer must remain disabled/);
  assert.match(watchdogTimer, /^OnActiveSec=2min$/m,
    '长时间运行的主机重新启用 timer 时也必须产生未来触发点');
  assert.match(watchdogTimer, /^OnUnitActiveSec=2min$/m,
    '首次触发后必须持续每两分钟调度');
  assert.match(script, /NextElapseUSecMonotonic/,
    'active (elapsed) 但没有下一次触发的 timer 必须阻止部署');
  assert.match(script, /watchdog timer is active but has no future trigger/);
  assert.match(script, /ExecMainStatus/,
    'systemd 将探针异常视为可预期退出时，部署仍必须显式要求首次探测为 0');
  assert.match(script, /touch "\$WATCHDOG_DIR\/deployment-check"/,
    '首次探测必须验证飞书私聊上报链路');
  assert.match(script, /HISTORY_ROOT=\/var\/www\/\.fbif-layout-history/);
  assert.match(script, /HISTORY_RETENTION=5/);
  assert.match(script, /finalize_release\(\)/);
  assert.match(script, /cp -a --reflink=auto -- "\$PUBLIC_TARGET" "\$history_pending\/public"/);
  assert.match(script, /cp -a --reflink=auto -- "\$WATCHDOG_SERVICE_TARGET"/,
    '历史快照必须保留 systemd 单元');
  assert.match(script, /cp -a --reflink=auto -- "\$API_SERVICE_TARGET"/);
  assert.match(script, /cp -a --reflink=auto -- "\$CONFIG_SCHEMA_TARGET"/);
  assert.match(script, /! -name '\.pending-\*'/,
    '保留策略不得把未完成的快照当成可恢复 release');
  const finalizeFunction = script.slice(
    script.indexOf('finalize_release()'),
    script.indexOf('\ncase "$ACTION"'),
  );
  assert.doesNotMatch(finalizeFunction, /cp .*ENV_TARGET|cp .*api\.env|cp .*publish\.env/,
    '历史快照不得保留明文密钥');
  assert.ok(
    finalizeFunction.indexOf('mv -- "$history_pending" "$history_target"') <
      finalizeFunction.indexOf('rm -rf -- "$BACKUP_ROOT" "$RELEASE_DIR"'),
    '完整历史快照落盘前不得删除事务备份',
  );
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
  const requiredAt = workflow.indexOf('for required in SSH_KEY KNOWN_HOSTS HOST USER DEPLOY_PATH ADMIN_PASSWORD RENDER_TOKEN');
  const firstRsyncAt = workflow.indexOf('rsync -avz');
  const activateAt = workflow.indexOf('remote_transaction activate');
  const finalizeAt = workflow.indexOf('remote_transaction finalize');
  const versionCheckAt = workflow.indexOf("'.commit == $sha and .build == $sha'");
  const readinessAt = workflow.indexOf('/api/publish/readiness');
  const imageUploadAt = workflow.indexOf("image_upload_json=$(curl");
  const acceptAt = workflow.indexOf('release_accepted=1');
  const unpauseAt = workflow.indexOf('remote_transaction unpause');
  const prepareMonitoringAt = workflow.indexOf('remote_transaction prepare-monitoring');
  const startMonitoringAt = workflow.indexOf('remote_transaction start-monitoring');

  assert.ok(requiredAt >= 0 && requiredAt < firstRsyncAt, 'secret preflight 必须早于任何 rsync');
  assert.match(workflow, /DEPLOY_PATH must be exactly \/var\/www\/wechat-layout/,
    '误配的部署路径必须在任何远程写入前失败');
  assert.match(workflow, /KNOWN_HOSTS: \$\{\{ secrets\.SERVER_KNOWN_HOSTS \}\}/,
    '部署必须使用预先核验的 SSH 主机公钥 Secret');
  assert.match(workflow, /ssh-keygen -F "\$HOST" -f ~\/\.ssh\/known_hosts/,
    '预置 known_hosts 必须覆盖目标 HOST');
  assert.doesNotMatch(workflow, /ssh-keyscan/,
    '运行时 ssh-keyscan 是 TOFU，不能作为生产主机身份依据');
  assert.doesNotMatch(workflow, /\$\{USER\}@\$\{HOST\}:\$\{DEPLOY_PATH\}\//,
    '静态文件不得直接覆盖 live DEPLOY_PATH');
  assert.doesNotMatch(workflow, /\$\{USER\}@\$\{HOST\}:\/var\/www\/render-service\//,
    'render 不得直接覆盖 live 目录');
  assert.match(workflow, /npm ci --omit=dev --no-audit --no-fund/,
    'render 依赖必须从 lockfile 可复现安装');
  assert.match(workflow, /playwright install --with-deps chromium firefox webkit/,
    '部署门禁必须安装三种浏览器引擎');
  assert.match(workflow, /E2E_CROSS_BROWSER: '1'/,
    'CI 必须启用 Firefox/WebKit 关键路径冒烟测试');
  assert.match(workflow, /npm run test:e2e:ci/,
    '无截图交互 E2E 必须进入部署门禁，不能只留在本机手跑');
  assert.match(workflow, /uses: actions\/upload-artifact@v4/);
  assert.match(workflow, /if: failure\(\)/,
    '浏览器失败时必须保留诊断产物');
  assert.match(workflow, /test-results\/e2e/);
  assert.match(workflow, /retention-days: 7/);
  assert.match(workflow, /uses: actions\/checkout@v6/,
    '官方 checkout Node 24 运行时版本必须固定，避免旧 action 弃用告警');
  assert.match(workflow, /uses: actions\/setup-go@v6/);
  assert.match(workflow, /cache-dependency-path: api\/go\.sum/,
    'Go 模块位于 api/，缓存不得再去仓库根目录找 go.sum');
  assert.match(workflow, /uses: actions\/setup-node@v6/);
  assert.match(workflow, /package-manager-cache: false/,
    'setup-node v6 不应依赖 packageManager 自动探测改变缓存行为');
  assert.match(workflow, /go-version: '1\.26\.5'/,
    '必须锁定已修复当前标准库漏洞的 Go patch 版本');
  assert.match(workflow, /npm run lint:js/);
  assert.match(workflow, /npm run test:coverage/);
  assert.match(workflow, /npm run audit:deps/);
  assert.match(workflow, /go vet \.\/\.\.\./);
  assert.match(workflow, /staticcheck@2026\.1/);
  assert.match(workflow, /govulncheck@v1\.6\.0/);
  assert.match(workflow, /shellcheck deploy\/scripts\/\*\.sh/);
  assert.match(workflow, /actual \+ 0 < 73/,
    'Go 覆盖率必须保持当前基线以上');
  assert.match(workflow, /permissions:\s*\n\s*contents: read/,
    'deploy workflow 必须使用最小 GitHub token 权限');
  assert.match(workflow, /remote_transaction rollback/);
  assert.match(workflow, /concurrency:\s*\n\s*group: fbifmp-layout-production\s*\n\s*cancel-in-progress: false/,
    '生产部署必须串行排队，不能让两个 run 同时 swap/rollback');
  assert.match(workflow, /trap 'on_deploy_error 143' TERM/,
    '公网验收窗口收到取消信号时也必须请求远端回滚');
  assert.ok(activateAt >= 0 && versionCheckAt > activateAt && finalizeAt > versionCheckAt,
    '公网版本验收必须发生在 activate 后、删除 rollback 备份前');
  assert.ok(imageUploadAt > versionCheckAt && readinessAt > imageUploadAt,
    '图片转存整链路必须在 activate 后、readiness 前的可回滚窗口验收');
  assert.ok(
    readinessAt > versionCheckAt &&
    prepareMonitoringAt > readinessAt &&
    acceptAt > prepareMonitoringAt &&
    unpauseAt > acceptAt &&
    startMonitoringAt > unpauseAt &&
    finalizeAt > startMonitoringAt,
    'readiness 和监控准备必须在暂停态验收；解暂停后才启动真实探测并 finalize',
  );
  assert.match(workflow, /DEPLOYMENT_WRITES_PAUSED=1/);
  assert.match(workflow, /ENV=production/);
  assert.match(workflow, /FBIF_CONFIG_SCHEMA_VERSION=1/);
  assert.match(workflow, /WECHAT_UPLOAD_ENDPOINT=http:\/\/127\.0\.0\.1:9000\/api\/wechat-upload/);
  assert.match(workflow, /\.image_upload\.configured == true/);
  assert.match(workflow, /'\{"urls":\["not-a-url"\]\}'/,
    '上传服务整链路探针不得写入 OSS');
  assert.match(workflow, /deploy\/systemd\//,
    'API/render systemd 单元必须与 release 一起上传');
  assert.match(workflow, /deploy\/config\//,
    '环境变量 schema 必须与 release 一起上传');
  assert.match(workflow, /\.config_schema\.matches == true/);
  assert.match(workflow, /\.workers_paused == true/);
  assert.match(workflow, /\.store_valid == true/);
  assert.match(workflow, /\.generated_files\.registry_present == true/);
  assert.match(workflow, /\.generated_files\.registry_hash_matches == true/);
  assert.match(workflow, /\.generated_files\.block_registry_present == true/);
  assert.match(workflow, /\.generated_files\.block_registry_hash_matches == true/);
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
    '/api/oss-upload',
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

test('Caddy sends every API to Go before static fallback and keeps only named legacy routes', async () => {
  const vhost = await projectFile('deploy/caddy/fbifmp-layout.caddy');
  const fallbackAt = vhost.indexOf('\n\thandle {');
  assert.ok(fallbackAt > 0, '必须存在最终静态 fallback');
  const routed = vhost.slice(0, fallbackAt);

  assert.match(routed, /@fbifAPI path \/api \/api\/\*/,
    '所有 API 必须先落 Go，未知路由 Go 返回 JSON 404');
  assert.match(routed, /handle \/api\/wechat-draft[\s\S]*?localhost:9000/);
  assert.match(routed, /handle \/api\/oss-presign[\s\S]*?localhost:9000/);
  assert.doesNotMatch(routed, /handle \/api\/(?:oss-upload|wechat-upload)[\s\S]*?localhost:9000/,
    '已由 Go 代理的上传端点不得保留永不命中的死路由');

  for (const route of ['/api/*', '/preview/*', '/mp/*', '/ft/*']) {
    assert.ok(routed.includes(route), `${route} 必须在静态 fallback 前代理`);
  }

  // 自动从非测试 Go 文件提取公开 API/preview 路由，确认 catch-all
  // 与非 API 预览页一起覆盖所有 Go 路由。
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
