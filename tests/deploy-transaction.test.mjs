import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { chmod, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const execFileAsync = promisify(execFile);
const root = new URL('../', import.meta.url);

async function projectFile(path) {
  return readFile(new URL(path, root), 'utf8');
}

// deploy.yml 里「Deploy transactionally」这一步的 run 脚本，去掉 YAML 缩进。
async function deployRunScript() {
  const workflow = await projectFile('.github/workflows/deploy.yml');
  const deployStepAt = workflow.indexOf('      - name: Deploy transactionally');
  const runMarker = '        run: |\n';
  const runAt = workflow.indexOf(runMarker, deployStepAt);
  return workflow.slice(runAt + runMarker.length)
    .split('\n')
    .map(line => line.startsWith('          ') ? line.slice(10) : line)
    .join('\n');
}

// 从 shell 脚本里切出一个顶格定义的函数，拿去单独跑。
function shellFunction(script, name) {
  const start = script.indexOf(`\n${name}() {\n`);
  assert.ok(start >= 0, `缺少函数 ${name}()`);
  const end = script.indexOf('\n}\n', start + 1);
  return script.slice(start + 1, end + 2);
}

test('deploy transaction shell is syntactically valid and restores every live surface', async () => {
  const scriptURL = new URL('../deploy/scripts/deploy-transaction.sh', import.meta.url);
  await execFileAsync('bash', ['-n', fileURLToPath(scriptURL)]);
  const script = await readFile(scriptURL, 'utf8');

  for (const surface of [
    'public', 'render', 'api', 'env', 'caddy',
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
  // 外检看门狗已迁往监控端（CLAUDE.md 规则 30）。部署链路这一步的职责从「安装并
  // 验收探针」变成「把残留摘干净」，测试跟着守新契约：必须摘、必须断言摘掉了、
  // 且回滚不得把它装回来——否则下次部署又会长出一个探上游死活的探针。
  assert.match(script, /cleanup_legacy_watchdog\(\)/);
  assert.doesNotMatch(script, /prepare_monitoring\(\)/,
    '不得恢复安装探针的旧流程');
  assert.doesNotMatch(script, /start_monitoring\(\)/,
    '不得恢复 enable timer 的旧流程');
  assert.doesNotMatch(script, /systemctl enable --now fbif-monitor-watchdog/,
    '部署不得重新启用已迁出的看门狗');
  assert.match(script, /rm -f -- \/etc\/systemd\/system\/fbif-monitor-watchdog\.service/);
  assert.match(script, /rm -f -- \/etc\/systemd\/system\/fbif-monitor-watchdog\.timer/);
  assert.match(script, /rm -rf -- \/opt\/fbif-monitor-watchdog/);
  assert.match(script, /legacy watchdog timer is still enabled after cleanup/,
    '摘不干净必须让部署失败，不能静默放过');
  assert.doesNotMatch(script, /restore_path watchdog/,
    '回滚不得恢复已迁出的看门狗');
  assert.match(script, /HISTORY_ROOT=\/var\/www\/\.fbif-layout-history/);
  assert.match(script, /HISTORY_RETENTION=5/);
  assert.match(script, /finalize_release\(\)/);
  assert.match(script, /cp -a --reflink=auto -- "\$PUBLIC_TARGET" "\$history_pending\/public"/);
  assert.match(script, /cp -a --reflink=auto -- "\$API_SERVICE_TARGET" "\$history_pending\/systemd\/fbif-api\.service"/,
    '历史快照必须保留 systemd 单元');
  assert.match(script, /cp -a --reflink=auto -- "\$RENDER_SERVICE_TARGET" "\$history_pending\/systemd\/fbif-render\.service"/,
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
  const runScript = await deployRunScript();
  await execFileAsync('bash', ['-n', '-c', runScript]);
  const requiredAt = workflow.indexOf('for required in SSH_KEY KNOWN_HOSTS HOST USER DEPLOY_PATH RULE_SUPER_ADMIN_OPEN_IDS AUTH_SESSION_SECRET');
  const sshTimeoutAt = workflow.indexOf('ConnectTimeout 15');
  const firstSSHAt = workflow.indexOf('ssh -i ~/.ssh/deploy_key');
  const firstRsyncAt = workflow.indexOf('rsync -avz');
  const activateAt = workflow.indexOf('remote_transaction activate');
  const finalizeAt = workflow.indexOf('remote_transaction finalize');
  const versionCheckAt = workflow.indexOf("'.commit == $sha and .build == $sha'");
  const readinessAt = workflow.indexOf('readiness_json=$(remote_transaction readiness)');
  const imageUploadAt = workflow.indexOf("image_upload_json=$(curl");
  const acceptAt = workflow.indexOf('release_accepted=1');
  const unpauseAt = workflow.indexOf('remote_transaction unpause');
  const prepareMonitoringAt = workflow.indexOf('remote_transaction prepare-monitoring');

  assert.ok(requiredAt >= 0 && requiredAt < firstRsyncAt, 'secret preflight 必须早于任何 rsync');
  assert.ok(sshTimeoutAt >= 0 && sshTimeoutAt < firstSSHAt,
    'SSH 连接与保活超时必须在第一次远端调用前生效');
  assert.match(workflow, /ConnectionAttempts 3/);
  assert.match(workflow, /ServerAliveInterval 15/);
  assert.match(workflow, /ServerAliveCountMax 3/);
  assert.match(workflow, /StrictHostKeyChecking yes/);
  assert.match(workflow, /DEPLOY_PATH must be exactly \/var\/www\/wechat-layout/,
    '误配的部署路径必须在任何远程写入前失败');
  assert.match(workflow, /KNOWN_HOSTS: \$\{\{ secrets\.SERVER_KNOWN_HOSTS \}\}/,
    '部署必须使用预先核验的 SSH 主机公钥 Secret');
  assert.match(workflow, /ssh-keygen -F "\$HOST" -f ~\/\.ssh\/known_hosts/,
    '预置 known_hosts 必须覆盖目标 HOST');
  assert.doesNotMatch(workflow, /remote_transaction start-monitoring/,
    '看门狗已迁出监控端，不得恢复启动探针的部署步骤');
  assert.doesNotMatch(workflow, /ssh-keyscan/,
    '运行时 ssh-keyscan 是 TOFU，不能作为生产主机身份依据');
  assert.doesNotMatch(workflow, /\$\{USER\}@\$\{HOST\}:\$\{DEPLOY_PATH\}\//,
    '静态文件不得直接覆盖 live DEPLOY_PATH');
  assert.doesNotMatch(workflow, /\$\{USER\}@\$\{HOST\}:\/var\/www\/render-service\//,
    'render 不得直接覆盖 live 目录');
  assert.match(workflow, /npm ci --omit=dev --no-audit --no-fund/,
    'render 依赖必须从 lockfile 可复现安装');
  assert.doesNotMatch(workflow, /playwright install/);
  assert.doesNotMatch(workflow, /E2E_CROSS_BROWSER/);
  assert.doesNotMatch(workflow, /npm run test:e2e/);
  assert.doesNotMatch(workflow, /firefox|webkit/);
  assert.doesNotMatch(workflow, /npm run lint:js/);
  assert.doesNotMatch(workflow, /npm run test:coverage/);
  assert.doesNotMatch(workflow, /go test/);
  assert.match(workflow, /timeout-minutes: 20/,
    '部署必须有硬超时，不能让装浏览器或卡住的考试占住生产队列');
  assert.match(workflow, /uses: actions\/checkout@v6/,
    '官方 checkout Node 24 运行时版本必须固定，避免旧 action 弃用告警');
  assert.match(workflow, /uses: actions\/setup-go@v6/);
  assert.match(workflow, /cache-dependency-path: api\/go\.sum/,
    'Go 模块位于 api/，缓存不得再去仓库根目录找 go.sum');
  assert.match(workflow, /go-version: '1\.26\.10'/,
    '必须锁定已修复当前标准库漏洞的 Go patch 版本（2026-10-22 GO-2026-6809 crypto/x509 Fixed in 1.26.10）');
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
    finalizeAt > unpauseAt,
    // 看门狗迁出后 start-monitoring 一步已删（CLAUDE.md 规则 30），但暂停态验收的
    // 顺序不变：readiness 与遗留探针清理都在写暂停窗口内做完、接受候选、再解暂停。
    'readiness 和遗留探针清理必须在暂停态完成；接受候选并解暂停后才 finalize',
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
  assert.match(workflow, /\.enrich_mode == "mimo"/);
  assert.doesNotMatch(workflow, /judge_configured|enrich_mode == "dual"/,
    '2026-10-07 起 AI 预填只走 MiMo，部署验收不得再要求双模型判决');
  assert.match(workflow, /Release already accepted; refusing automatic rollback/,
    'worker 恢复后不得再自动回滚旧二进制');
  for (const contract of [
    '/api/health',
    '/version.json?deploy=${GITHUB_SHA}',
    '/api/rules/admin/presets',
    '/api/publish/jobs',
    '/api/feishu/card-callback',
    '/api/oss-upload',
    'readiness_json=$(remote_transaction readiness)',
    'unpaused_readiness=$(remote_transaction readiness)',
  ]) {
    assert.ok(workflow.includes(contract), `缺少部署验收：${contract}`);
  }
});

// FBIF 中台统一管共用密钥（2026-10-26）：这 10 个变量（渲染服务 1 个）由中台本机代理写进
// /etc/fbif-hub/env/layout.env、render.env，systemd drop-in 让服务最后读、同名为准。CI 再从
// GitHub Secrets 写一份，中台换值后 GitHub 那份就过期了，所以 CI 既不持有也不写它们。
const HUB_LAYOUT_VARIABLES = [
  'CLOUDCONVERT_API_KEY', 'FEISHU_LOGIN_APP_SECRET', 'PUBLISH_ADMIN_SERVICE_TOKEN', 'RENDER_TOKEN',
  'DISTRIBUTION_API_TOKEN', 'FEISHU_APP_SECRET', 'FOODTALKS_TOKEN', 'HUB_KEY',
  'PUBLISH_SYNC_SERVICE_TOKEN', 'TIKHUB_API_KEY',
];

test('deploy workflow neither holds nor writes hub-managed variables; readiness goes through the server', async () => {
  const workflow = await projectFile('.github/workflows/deploy.yml');
  const runScript = await deployRunScript();
  for (const name of HUB_LAYOUT_VARIABLES) {
    assert.doesNotMatch(workflow, new RegExp(`secrets\\.${name}\\b`), `${name} 由中台下发，CI 不得再从 GitHub Secrets 取`);
    assert.doesNotMatch(workflow, new RegExp(`\\b${name}=%s`), `${name} 不得再写进 api.env / render.env`);
  }
  const required = runScript.match(/^for required in (.+); do$/m);
  assert.ok(required, '必须保留 secret preflight');
  for (const name of HUB_LAYOUT_VARIABLES) {
    assert.ok(!required[1].split(' ').includes(name), `${name} 不得再是必需的 GitHub Secret`);
  }
  for (const name of ['RULE_SUPER_ADMIN_OPEN_IDS', 'AUTH_SESSION_SECRET', 'AUTH_PASSWORD_USERS_B64', 'FEISHU_LOGIN_APP_ID']) {
    assert.ok(required[1].split(' ').includes(name), `${name} 不归中台，仍由 CI 必需`);
  }
  assert.match(workflow, /LIBREOFFICE_TOKEN: \$\{\{ secrets\.LIBREOFFICE_TOKEN \}\}/);
  assert.doesNotMatch(workflow, /X-Publish-Admin-Token/,
    '管理员服务令牌只在服务器的中台文件里，CI 不得再从公网带令牌调 readiness');
  assert.doesNotMatch(workflow, /\$\{SITE_ORIGIN\}\/api\/publish\/readiness/);
  assert.equal(runScript.match(/\$\(remote_transaction readiness\)/g)?.length, 2,
    '暂停态和解暂停后的两次 readiness 都经事务脚本在服务器本机取');
});

test('a failure inside $(...) during verification rolls back exactly once', async () => {
  // set -E 让 ERR trap 也在命令替换的子 shell 里触发；子 shell 若也回滚并删 staging，主 shell 再回滚
  // 就找不到事务脚本、误报 ROLLBACK FAILED。readiness 改成 $(remote_transaction readiness) 后这条路必走。
  const runScript = await deployRunScript();
  const directory = await mkdtemp(join(tmpdir(), 'fbif-deploy-trap-'));
  const callLog = join(directory, 'calls.log');
  const harness = [
    'set -Eeuo pipefail',
    'remote_transaction() { echo "remote_transaction $1" >> "$CALL_LOG"; [ "$1" != readiness ]; }',
    'cleanup_staging() { echo cleanup_staging >> "$CALL_LOG"; }',
    'DEPLOY_ID=test-deploy',
    'RELEASE_DIR=/var/www/.fbif-layout-releases/test-deploy',
    'transaction_started=1',
    'release_accepted=0',
    shellFunction(runScript, 'on_deploy_error'),
    "trap 'on_deploy_error $?' ERR",
    'readiness_json=$(remote_transaction readiness)',
    'echo "unreachable $readiness_json"',
  ].join('\n');
  try {
    await assert.rejects(execFileAsync('bash', ['-c', harness], { env: { ...process.env, CALL_LOG: callLog } }));
    const calls = (await readFile(callLog, 'utf8')).trim().split('\n');
    assert.deepEqual(calls, ['remote_transaction readiness', 'remote_transaction rollback', 'cleanup_staging']);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('P0 quality gate runs before every deploy, on Chrome only with a cached browser', async () => {
  const workflow = await projectFile('.github/workflows/p0-monitoring-contract.yml');
  const deploy = await projectFile('.github/workflows/deploy.yml');
  // 只有 main 一个分支（2026-10-08 用户裁定）：推 main 由 deploy 调用本工作流考试，通过才上线。
  assert.match(workflow, /workflow_call:/);
  assert.match(deploy, /uses: \.\/\.github\/workflows\/p0-monitoring-contract\.yml/,
    '推 main 必须先跑完整 P0 考试');
  assert.match(deploy, /needs: \[p0\]/, '考试不过不得进入部署');
  assert.doesNotMatch(workflow, /push:\s*\n\s*branches:\s*\n\s*-\s*main/,
    '考试由 deploy 调用，不另挂 push 触发，避免同一提交考两遍');
  assert.match(workflow, /backend-and-watchdog:/,
    '分支保护依赖这个 job 名，不得改名');
  assert.match(workflow, /gofmt -l/);
  assert.match(workflow, /go vet \.\/\.\.\./);
  assert.match(workflow, /staticcheck@2026\.1/);
  assert.match(workflow, /govulncheck@v1\.6\.0/);
  assert.match(workflow, /actual \+ 0 < 73/);
  assert.match(workflow, /go test -race/);
  assert.match(workflow, /shellcheck deploy\/scripts\/\*\.sh/);
  assert.match(workflow, /npm run lint:js/);
  assert.match(workflow, /npm run test:coverage/);
  assert.match(workflow, /npm run audit:deps/);
  assert.match(workflow, /playwright install --with-deps chromium/);
  assert.doesNotMatch(workflow, /playwright install --with-deps chromium firefox webkit/);
  assert.doesNotMatch(workflow, /E2E_CROSS_BROWSER/);
  assert.match(workflow, /~\/\.cache\/ms-playwright/,
    'Chrome 必须按 lockfile 缓存，不能每次重下');
  assert.match(workflow, /npm run test:e2e:ci/);
  assert.match(workflow, /uses: actions\/upload-artifact@v4/);
  assert.match(workflow, /test-results\/e2e/);
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

// 审核群只发卡片（2026-08-09 用户裁定，取代 2026-08-06 的早间播报）：
// 「这种播报不要发群里面，群里面只发卡片消息。因为大部分的运营他们不需要知道
// 这些东西，会给他们带来误解。」部署必须把残留的播报单元幂等摘除，且回滚不恢复。
test('deploy removes the review-chat broadcast timer and never restores it', async () => {
  const script = await projectFile('deploy/scripts/deploy-transaction.sh');
  assert.match(script, /cleanup_group_broadcast\(\)/);
  assert.match(script, /cleanup_group_broadcast\n/, 'prepare-monitoring 必须真的调用它');
  assert.match(script, /systemctl disable fbif-monitoring-report\.timer/);
  assert.match(script, /rm -f -- \/etc\/systemd\/system\/fbif-monitoring-report\.timer/);
  assert.match(script, /group broadcast timer is still enabled after cleanup/,
    '摘不干净必须让部署失败——残留单元会让播报第二天照发');

  assert.doesNotMatch(script, /systemctl enable --now fbif-monitoring-report\.timer/,
    '不得再安装启用播报 timer');
  assert.doesNotMatch(script, /install_file monitoring_report_/,
    '不得再把播报单元装进生产');
  assert.doesNotMatch(script, /restore_path monitoring_report_/,
    '只摘不恢复：回滚也不该让播报重新长回群里');

  await assert.rejects(projectFile('deploy/systemd/fbif-monitoring-report.timer'),
    '播报 unit 文件必须已从仓库删除');
});

// 事务脚本要 root 才跑，这里把中台相关的函数切出来单独跑；stat、systemctl、curl 用假的，
// 只验逻辑：中台文件的检查、readiness 只吐响应 JSON、令牌不进命令行和输出。
async function hubHarness() {
  const script = await projectFile('deploy/scripts/deploy-transaction.sh');
  const directory = await mkdtemp(join(tmpdir(), 'fbif-hub-env-'));
  const bin = join(directory, 'bin');
  await mkdir(bin);
  const stubs = {
    stat: 'printf "%s\\n" "$FAKE_STAT"',
    systemctl: 'printf "%s\\n" "$FAKE_ENV_FILES"',
    curl: [
      'for arg in "$@"; do printf "%s\\n" "$arg"; done > "$CURL_ARGV_LOG"',
      'cat > "$CURL_STDIN_LOG"',
      'printf "%s" "$FAKE_READINESS"',
    ].join('\n'),
  };
  for (const [name, body] of Object.entries(stubs)) {
    await writeFile(join(bin, name), `#!/bin/sh\n${body}\n`, 'utf8');
    await chmod(join(bin, name), 0o755);
  }
  const readinessURL = script.match(/^API_READINESS_URL=(.+)$/m)?.[1];
  const layoutEnv = join(directory, 'layout.env');
  const run = (body, env = {}) => execFileAsync('bash', ['-c', [
    'set -Eeuo pipefail',
    shellFunction(script, 'die'),
    shellFunction(script, 'hub_env_value'),
    shellFunction(script, 'require_hub_env'),
    shellFunction(script, 'publish_readiness'),
    `HUB_LAYOUT_ENV='${layoutEnv}'`,
    `API_READINESS_URL='${readinessURL}'`,
    body,
  ].join('\n')], {
    env: {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      FAKE_STAT: '0 600',
      FAKE_ENV_FILES: `/var/www/api/publish.env (ignore_errors=no)\n/var/www/api/.env (ignore_errors=no)\n${layoutEnv} (ignore_errors=no)`,
      CURL_ARGV_LOG: join(directory, 'curl-argv.log'),
      CURL_STDIN_LOG: join(directory, 'curl-stdin.log'),
      ...env,
    },
  });
  return { script, directory, layoutEnv, readinessURL, run };
}

test('hub env files are checked before any env validation and overlaid like systemd', async () => {
  const script = await projectFile('deploy/scripts/deploy-transaction.sh');
  const envTargetAt = script.indexOf('ENV_TARGET=$API_DIR/.env');
  assert.ok(envTargetAt > 0);
  const header = script.slice(envTargetAt, script.indexOf('\nmarker() {'));
  assert.match(header, /^HUB_LAYOUT_ENV=\/etc\/fbif-hub\/env\/layout\.env$/m, '中台文件路径定义在顶部，和 ENV_TARGET 放一起');
  assert.match(header, /^HUB_RENDER_ENV=\/etc\/fbif-hub\/env\/render\.env$/m);

  const validate = shellFunction(script, 'validate_release');
  const layoutCheckAt = validate.indexOf('require_hub_env fbif-api "$HUB_LAYOUT_ENV"');
  const renderCheckAt = validate.indexOf('require_hub_env fbif-render "$HUB_RENDER_ENV"');
  const firstValidateAt = validate.indexOf('python3 "$RELEASE_DIR/validate-env.py"');
  assert.ok(layoutCheckAt > 0 && renderCheckAt > 0 && layoutCheckAt < firstValidateAt && renderCheckAt < firstValidateAt,
    '中台文件缺失、权限不对必须在校验 env 之前给出中文报错');
  assert.match(validate, /validate-env\.py" --overlay "\$HUB_LAYOUT_ENV" \\\n\s+"\$RELEASE_DIR\/config\/env-schema\.json" api_managed "\$RELEASE_DIR\/api\.env"/);
  assert.match(validate, /validate-env\.py" --overlay "\$HUB_LAYOUT_ENV" \\\n\s+"\$RELEASE_DIR\/config\/env-schema\.json" api_publish \/var\/www\/api\/publish\.env/);
  assert.match(validate, /validate-env\.py" --overlay "\$HUB_RENDER_ENV" \\\n\s+"\$RELEASE_DIR\/config\/env-schema\.json" render "\$RELEASE_DIR\/render\.env"/);
  assert.match(validate, /hub_env_value "\$HUB_LAYOUT_ENV" RENDER_TOKEN\)" == "\$\(hub_env_value "\$HUB_RENDER_ENV" RENDER_TOKEN/,
    'API 与渲染服务各读一份中台文件，RENDER_TOKEN 必须一致');
  const tokenReads = [...script.matchAll(/hub_env_value "\$(\w+)"/g)].map(match => match[1]);
  assert.ok(tokenReads.length >= 3 && tokenReads.every(name => name === 'HUB_LAYOUT_ENV' || name === 'HUB_RENDER_ENV'),
    '令牌只从中台文件读，不从 .env / render-service.env 读');
  assert.doesNotMatch(script, /(?:grep|sed|cut|awk)[^\n]*_(?:TOKEN|SECRET|KEY)\b/,
    '事务脚本不得用文本工具从 env 文件里抠密钥值');
  assert.match(script, /^  readiness\)\n    publish_readiness\n    ;;$/m);
});

test('require_hub_env fails in Chinese when the hub file is missing, not root 0600, or not read last', async () => {
  const { directory, layoutEnv, run } = await hubHarness();
  const check = `require_hub_env fbif-api "$HUB_LAYOUT_ENV"`;
  try {
    await assert.rejects(run(check), error => {
      assert.match(error.stderr, /缺中台文件 .*layout\.env：它由 FBIF 中台的本机代理（hub-agent）写/);
      return true;
    });

    await writeFile(layoutEnv, 'PUBLISH_ADMIN_SERVICE_TOKEN=x\n', 'utf8');
    for (const fakeStat of ['1000 600', '0 644', '0 640']) {
      await assert.rejects(run(check, { FAKE_STAT: fakeStat }), error => {
        assert.match(error.stderr, /必须归 root、权限 600/);
        assert.match(error.stderr, /中台代理写/);
        return true;
      });
    }

    await assert.rejects(run(check, {
      FAKE_ENV_FILES: `${layoutEnv} (ignore_errors=no)\n/var/www/api/publish.env (ignore_errors=no)`,
    }), error => {
      assert.match(error.stderr, /最后读的不是中台文件/);
      assert.match(error.stderr, /zz-fbif-hub\.conf/);
      return true;
    });
    await assert.rejects(run(check, { FAKE_ENV_FILES: '/var/www/api/.env (ignore_errors=no)' }));

    const linked = join(directory, 'linked.env');
    await symlink(layoutEnv, linked);
    await assert.rejects(run(`require_hub_env fbif-api '${linked}'`), /缺中台文件/,
      '中台代理是改名落盘的普通文件，软链接不认');

    await run(check);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('readiness action prints only the response JSON and never exposes the hub token', async () => {
  const { directory, layoutEnv, readinessURL, run } = await hubHarness();
  const token = 'hub-admin-token-must-stay-secret';
  const readiness = '{"ready":true,"workers_paused":true}';
  try {
    assert.equal(readinessURL, 'http://127.0.0.1:9001/api/publish/readiness', 'readiness 只在服务器本机取');
    await writeFile(layoutEnv, [
      '# 由 FBIF 中台本机代理写，别手改',
      'PUBLISH_ADMIN_SERVICE_TOKEN=stale-earlier-line',
      'RENDER_TOKEN=render-token',
      `PUBLISH_ADMIN_SERVICE_TOKEN=${token}`,
      '',
    ].join('\n'), 'utf8');
    const result = await run('publish_readiness', { FAKE_READINESS: readiness });
    assert.equal(result.stdout, readiness, '标准输出只能是响应 JSON，jq 才拿得到纯 JSON');
    assert.doesNotMatch(result.stdout + result.stderr, new RegExp(token));

    const argv = await readFile(join(directory, 'curl-argv.log'), 'utf8');
    assert.doesNotMatch(argv, new RegExp(token), '令牌不得出现在 curl 命令行（ps 看得到）');
    assert.ok(argv.split('\n').includes(readinessURL));
    assert.ok(argv.split('\n').includes('--fail'), 'readiness 非 2xx 必须让部署失败');
    const headers = await readFile(join(directory, 'curl-stdin.log'), 'utf8');
    assert.equal(headers, `X-Publish-Admin-Token: ${token}\n`, '令牌经标准输入当请求头，同名取最后一行（和 systemd 一样）');

    await writeFile(layoutEnv, 'RENDER_TOKEN=render-token\n', 'utf8');
    await rm(join(directory, 'curl-argv.log'));
    await assert.rejects(run('publish_readiness', { FAKE_READINESS: readiness }), error => {
      assert.equal(error.stdout, '');
      assert.match(error.stderr, /里没有 PUBLISH_ADMIN_SERVICE_TOKEN/);
      return true;
    });
    await assert.rejects(readFile(join(directory, 'curl-argv.log'), 'utf8'), '缺令牌时不得发请求');
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
