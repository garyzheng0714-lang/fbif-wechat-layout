// Package auth 接飞书集成平台（AnyCross）单点登录，路线 A 统一接入模板，v7：登录页和账号密码在登录中心（login.fbif.com），
// 飞书登录照旧本项目直连集成平台；日志、SSO 应用密钥、停用名单、账号核对都经 FBIF 中台。
// 契约、接入步骤和合规测试见 feishu-login-guide 技能 templates/backend/anycross-sso/README.md；
// 改了这个文件必须重跑 conformance/selftest.sh。行为与 node/sso.ts.template 一一对应。
//
// 依赖：github.com/coreos/go-oidc/v3、golang.org/x/oauth2；Go 1.24+（路由用方法前缀）。
package auth

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	loginPath           = "/auth/feishu/fbif/login"
	callbackPath        = "/auth/feishu/fbif/callback"
	logoutPath          = "/auth/feishu/logout"
	mePath              = "/api/me"
	blocklistPath       = "/internal/auth/blocklist-changed"
	accountsChangedPath = "/internal/auth/accounts-changed"
	healthPath          = "/internal/auth/health"
	// 登录中心：没登录去 /auth/login（跳到中心的登录页），账号密码登完回 /auth/center/callback，登录页上的飞书按钮回 /auth/center/feishu。
	centerLoginPath    = "/auth/login"
	centerCallbackPath = "/auth/center/callback"
	centerFeishuPath   = "/auth/center/feishu"
	// 浏览器里经登录中心的飞书登录一处退出、处处退出：中心把退出令牌送到这里（OpenID Connect Back-Channel Logout 1.0）；
	// 本项目退出带 sid 的会话时调中心的 /api/logout，中心再通知其他项目。
	backchannelPath  = "/auth/backchannel-logout"
	centerLogoutPath = "/api/logout"
	backchannelEvent = "http://schemas.openid.net/event/backchannel-logout"

	sessionCookie   = "__Host-fbif_sid"
	txnCookiePrefix = "__Host-fbif_sso_"
	// 刚在本项目退出：这之后 5 分钟内去登录中心都带 prompt=login，出登录页，不让中心自动走飞书又登回来；登录成功就清掉。
	signedOutCookie = "__Host-fbif_signed_out"
	signedOutTTL    = 5 * time.Minute

	sessionIdle  = 30 * 24 * time.Hour
	touchEvery   = time.Hour
	defaultCheck = 5 * time.Minute
	// 兜底对账：事件（账号表自动化、名单提醒）丢了也不会一直错下去。默认 6 小时一轮，按 10 分钟～24 小时算。
	defaultReconcile = 6 * time.Hour
	minReconcile     = 10 * time.Minute
	maxReconcile     = 24 * time.Hour
	outageGrace      = 15 * time.Minute
	txnTTL           = 10 * time.Minute
	outageBackoff    = 30 * time.Second
	defaultGap       = 30 * time.Second
	syncAttempts     = 4
	upstreamLimit    = 20 * time.Second
	// 停用名单经中台读：一轮最多试 4 次，退避从 SSO_SYNC_RETRY 的 1/60 起翻倍（默认 5 秒起）；一轮用完还没成，
	// 隔 SSO_SYNC_RETRY（默认 5 分钟）再来一轮，直到成功，不等下一条提醒或兜底对账。
	defaultSyncRetry = 5 * time.Minute
	// 中台那头最多等 70 秒（全中台合并拉取，两次至少隔 30 秒），这一路给 90 秒。
	blocklistTimeout = 90 * time.Second
	// 中台上次真正拉到集成平台的名单超过这么久，健康检查报 blocklist_stale（不挡登录）。
	blocklistStaleAfter = 12 * time.Hour

	// SSO 应用密钥由中台按系统发，只放内存：手上没有能用的时 5 秒内最多问一次中台，登录最多等 5 秒；
	// 手上有、距上次问超过 10 分钟，有人用时在后台顺手再问；距最后一次从中台拿到超过 SSO_SECRET_MAX_AGE（默认 24 小时）就不用了。
	secretAskGap     = 5 * time.Second
	secretWait       = 5 * time.Second
	secretRefresh    = 10 * time.Minute
	defaultSecretAge = 24 * time.Hour
	// 集成平台不认的那个版本 10 分钟内不再用；过了再试一次，集成平台误报时不用重启就能恢复。
	secretRejectFor = 10 * time.Minute

	// 账号密码、日志、SSO 密钥都经 FBIF 中台（One-Api 第二实例的 /hub-auth/<系统>/…）。中台那头要查飞书，一次登录最多十来秒。
	hubTimeout = 10 * time.Second
	// 中台回包里的核验时间和本机差得超过这么多就不认（时钟歪了、回包被缓存或重放）。
	hubFresh = time.Minute
	// 系统开关的提醒：同一时间只拉一次，两次之间至少隔这么久。
	statePullGap = 5 * time.Second

	// 待写队列：有东西进来就写，写的时候新进来的攒着下一次一起写（一次最多 500 行）；失败才退避。
	// 日志超过 1 万条就丢最旧的（记 stderr）；积压超过 15 分钟或 1000 条，健康检查报警。
	outboxMaxLogs      = 10_000
	outboxBatch        = 500
	outboxMaxBackoff   = 5 * time.Minute
	outboxStaleAfter   = 15 * time.Minute
	outboxBacklogLimit = 1000
	// 一个进程最近一分钟最多新增这么多行请求日志、登录失败日志，多的丢掉（防扫描器刷满日志表：飞书一张表最多两三万到五万行）。
	// 登录失败不用登录就能造出来，额度单独算、比请求少。
	requestRowsPerMinute   = 200
	loginFailRowsPerMinute = 30
	droppedWarnFor         = 15 * time.Minute
	// 一样的日志行（请求：方法、路径、状态码、人；登录失败：方式、IP、账号、原因）10 秒内最多新起一行，
	// 这期间再来的攒进那一行的「次数」，到点再写出去。
	logMergeWindow = 10 * time.Second

	// 日志表永远不删（2026-10-16 用户裁定「永远不做删除」）：启动时和每写 500 行查一次总行数，
	// 到 SSO_LOG_WARN_ROWS（默认旗舰版单表上限 5 万行的 90%）就在健康检查里提醒，由人在飞书给这张表扩容。
	defaultLogWarnRows = 45_000
	logCountEvery      = 500

	// 账号表变更提醒：同一行同时只处理一次，全局每分钟最多处理这么多次（处理完再来一遍也算一次），伪造的提醒再多也只多问几次中台。
	accountsChangedPerMinute = 60

	// 登录中心 id_token 里的中台核验时间：最早 2 分钟前（中台核密码 → 跳回本项目 → 换码），最晚比本机快 1 分钟（时钟误差）。
	centerVerifiedBefore = 2 * time.Minute
	centerVerifiedAfter  = time.Minute

	// 登录中心签的 sid_hint、退出令牌：发证时间最多比本机快 1 分钟（时钟误差）。
	centerClockSkew = time.Minute
	// 退出带 sid 的会话后告诉登录中心：一次最多等 5 秒；连不上、5xx 在后台退避重试（5 秒起翻倍、最长 5 分钟），1 小时还不成就放弃（记 stderr）。
	centerLogoutTimeout  = 5 * time.Second
	centerLogoutFirstGap = 5 * time.Second
	centerLogoutMaxGap   = 5 * time.Minute
	centerLogoutGiveUp   = time.Hour
	// 退出令牌整个进程每分钟最多收这么多张：不用登录就能送来，验一张要找中心取一次公钥。
	backchannelPerMinute = 120
)

// 账号密码登录的人没有集成平台身份，company_code 记成这个，和 FBIF、富的分开。
const accountCompanyCode = "account"

// sessionGen 记在每个新会话上（Session.Gen）：有它的会话是浏览器一处登录处处登录（2026-11-11）上线后建的。
const sessionGen = 1

// staleBrowserSession：一处登录处处登录上线前在浏览器里飞书登录的会话，没挂登录中心（没有 SID），别的项目退出管不到它
// （2026-11-11 用户在 Tools 退出，排版工具 11-10 登的会话照样能用）。当没登录、请他经登录中心重登一次：中心认得这个浏览器
// 就直接送去飞书登录、不用点，登完就挂上了。飞书客户端里的、账号密码的会话照旧。
func staleBrowserSession(sess *Session, r *http.Request) bool {
	return sess.Gen == 0 && !sess.Account && sess.SID == "" && !feishuClientUA.MatchString(r.UserAgent())
}

// 同一个人在 FBIF 和富的是两个不同的 sub，靠 company_code 区分。
// 应用没开「关联组织访问」时，集成平台不在 id_token 里写 company_code（实测），这时按签发方地址末尾的企业编号认。
var allowedCompanyCodes = map[string]bool{
	"FJY632B9P3E": true, // FBIF
	"FMZYRL01RNE": true, // 富的
}

const (
	methodFeishu  = "飞书"
	methodAccount = "账号密码"

	// 日志「类型」「结果」两列的值。
	typeLogin   = "登录"
	typeLogout  = "退出"
	typeRevoked = "被收回"
	typeRequest = "请求"
	resultOK    = "成功"
	resultFail  = "失败"

	// 代码里的事件名，写日志时拆成「类型」「结果」。
	eventLoginOK   = "登录成功"
	eventLoginFail = "登录失败"
	eventLogout    = "退出"
	eventRevoked   = "被收回"
	eventRequest   = "请求"

	// 可合并的日志行分两类，各有每分钟的额度（合并键的第一段）。
	rowRequest   = "request"
	rowLoginFail = "login_fail"

	// 本应用的账号登录被中台停了：日志「被收回」和登录失败的原因。
	revokedReason = "本应用的账号登录已停用"
	// v5 的会话存的是 16 位十六进制的密码指纹，和中台的凭据版本对不上：第一次核对时请他重新登录一次。
	legacyReason = "登录方式升级，请重新登录"
)

// 退役的变量：v5 直连多维表格的那几个，和收进中台的 SSO 应用密钥、停用名单地址。见到就启动失败，免得以为还在用、或把秘密留在服务器上。
var retiredEnv = []string{"SSO_ACCOUNTS_TABLE", "SSO_LOG_TABLE", "SSO_APP_RECORD", "SSO_FEISHU_API", "SSO_FEISHU_APP_ID", "SSO_FEISHU_APP_SECRET", "SSO_CLIENT_SECRET", "SSO_BLOCKLIST_URL"}

// 空白按 JS 正则的 \s 算（Go 的 \s 只有 ASCII 那几个，这里补上 \v、全角空格这些），和 Node 版认的一样。
var (
	// 中台地址只认本机明文（127.0.0.1、localhost、[::1] 带端口）或 https，网址里不能带账号密码。
	hubURLFormat = regexp.MustCompile(`^(?:http://(?:127\.0\.0\.1|localhost|\[::1\]):[0-9]{1,5}|https://[^/?#@\s\v\pZ\x{FEFF}]+)/hub-auth/[a-z][a-z0-9-]{0,40}$`)
	hubKeyFormat = regexp.MustCompile(`^pqk_[A-Za-z0-9_-]{16,200}$`)
	// 中台报的表结构问题码，原样进健康检查。
	hubProblemFormat = regexp.MustCompile(`^(accounts|log)_column:[^\s\v\pZ\x{FEFF}]{1,40}$`)
	// 中台给的凭据版本（密码的指纹，按系统分开，倒推不出密码）。
	credentialFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	// 中台给的 SSO 密钥版本（带盐 HMAC 的前 16 位）。
	secretVersionFormat = regexp.MustCompile(`^[0-9a-f]{16}$`)
	// 登录中心的地址：https 的域名，或本机明文带端口（合规测试、本机调试），不带路径。
	centerIssuerFormat = regexp.MustCompile(`^(?:https://[A-Za-z0-9.-]+(?::[0-9]{1,5})?|http://(?:127\.0\.0\.1|localhost|\[::1\]):[0-9]{1,5})$`)
	// 中台发的登录中心客户端密钥。
	centerSecretFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{32,200}$`)
	// 登录中心给的浏览器会话编号（中心 cookie 的哈希）。
	sidFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// loginHints 是 /auth/login?error= 能带给登录页的出错代号（登录页只认这几个）。
var loginHints = map[string]bool{"denied": true, "unauthorized_tenant": true, "disabled": true, "expired": true, "failed": true, "unavailable": true}

// Identity 是集成平台给的身份。认人用 Key()，不要用邮箱：富的账号邮箱为空。
// 账号密码登录的人 CompanyCode 是 "account"，Sub 是共用账号表里那一行的 record_id（改账号名还是同一个人），Name 是账号，
// Apps 是那一行「能用的应用」里的全部 record_id（每个核对周期由中台刷新），工具入口这类项目可以按它过滤；飞书登录的人没有 Apps，表示不受限。
type Identity struct {
	CompanyCode string   `json:"company_code"`
	Sub         string   `json:"sub"`
	Name        string   `json:"name"`
	Email       string   `json:"email,omitempty"`
	Picture     string   `json:"picture,omitempty"`
	Apps        []string `json:"apps,omitempty"`
}

func (i Identity) Key() string { return i.CompanyCode + ":" + i.Sub }

// User 是已登录请求里的当前用户，用 CurrentUser(r) 取。
type User struct {
	ID string
	Identity
}

type Session struct {
	IDHash      string    `json:"id_hash"`
	UserID      string    `json:"user_id"`
	Identity    Identity  `json:"identity"`
	RefreshSeal string    `json:"refresh_seal"`
	CreatedAt   time.Time `json:"created_at"`
	CheckedAt   time.Time `json:"checked_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Version     int64     `json:"version"`
	// Account 为 true 是账号密码登录的会话：没有 refresh_token，不找集成平台核对，也不归停用名单管，改找中台核对。
	Account bool `json:"account,omitempty"`
	// AccountCredential 是中台给的凭据版本（密码的指纹，不是密码本身）：表里改了密码中台给的就变，会话就下线。
	AccountCredential string `json:"account_credential,omitempty"`
	// SID 是登录中心给的浏览器会话编号：只有在浏览器里经登录中心飞书登录的会话有。同一个 SID 的会话（同一个浏览器里各项目的）
	// 一处退出、处处退出；账号密码会话、飞书客户端里登的、不经中心直接飞书登录的没有，退出只退本项目。
	SID string `json:"sid,omitempty"`
	// Gen 是建会话的模板代数（sessionGen）。没有的（0）是浏览器一处登录处处登录上线前留下的，见 staleBrowserSession。
	Gen int `json:"gen,omitempty"`
}

type BlockEntry struct {
	Reason string `json:"reason"`
	Since  string `json:"since,omitempty"`
}

type Blocklist struct {
	Revision int64                 `json:"revision"`
	SyncedAt time.Time             `json:"synced_at"`
	Entries  map[string]BlockEntry `json:"entries"`
}

var (
	ErrConflict       = errors.New("sso: session changed or gone")
	ErrStaleBlocklist = errors.New("sso: blocklist older than the stored one")
	ErrNotAllowed     = errors.New("sso: user not allowed in this project")
)

// Store 是会话和停用名单的存储。多实例部署必须用所有实例共享的库实现它，
// 踢人和拒绝登录才能所有实例一起生效。
type Store interface {
	CreateSession(ctx context.Context, s Session) error
	// GetSession 找不到返回 nil, nil。
	GetSession(ctx context.Context, idHash string) (*Session, error)
	// UpdateSession 只在库里版本仍等于 expectVersion 时写入，并把版本加一；
	// 会话已删除或已被别处改过返回 ErrConflict。
	UpdateSession(ctx context.Context, s Session, expectVersion int64) error
	// DeleteSession 返回被删的会话，不存在返回 nil, nil。
	DeleteSession(ctx context.Context, idHash string) (*Session, error)
	// DeleteSessionsBySub 删掉这些 sub 的全部会话并返回它们；账号密码会话（Account=true）不归名单管，不删。
	DeleteSessionsBySub(ctx context.Context, subs map[string]BlockEntry) ([]Session, error)
	// DeleteSessionsBySID 删掉 Session.SID 等于 sid 的全部会话并返回它们（登录中心送来的退出令牌：一处退出、处处退出）。
	DeleteSessionsBySID(ctx context.Context, sid string) ([]Session, error)
	// AccountSessions 返回全部账号密码会话（Account=true；sub 是账号表那一行的 record_id），变更提醒、被停和兜底对账用。
	AccountSessions(ctx context.Context) ([]Session, error)
	// BlocklistRevision 返回库里名单的版本；synced=false 表示从没成功同步过。
	BlocklistRevision(ctx context.Context) (revision int64, synced bool, err error)
	// BlockedEntry 查一个 sub 在不在名单里，不在返回 nil, nil。
	BlockedEntry(ctx context.Context, sub string) (*BlockEntry, error)
	// SaveBlocklist 整份替换名单，但只在 b.Revision 不小于库里版本时写入，否则返回 ErrStaleBlocklist。
	// 比较和写入必须是一个原子操作：多实例时晚到的旧名单不能盖掉新名单。
	SaveBlocklist(ctx context.Context, b Blocklist) error
}

type Options struct {
	Store Store
	// OnLogin 把身份对上本项目的用户（按 Identity.Key() 查找或新建），返回本项目用户 ID。
	// 返回 ErrNotAllowed 表示本项目不让这个人进。账号密码登录同样经过它，company_code 是 "account"。
	OnLogin func(ctx context.Context, id Identity) (userID string, err error)
}

type SSO struct {
	oauth          oauth2.Config // 不带密钥：每次换码、刷新用手上的 SSO 密钥现配一份（oauthWith）
	verifier       *oidc.IDTokenVerifier
	issuerCompany  string // SSO_ISSUER 末尾的企业编号：id_token 里没写 company_code 时按它认
	userInfoURL    string
	revokeURL      string
	aead           cipher.AEAD
	checkEvery     time.Duration
	reconcileEvery time.Duration
	syncMinGap     time.Duration
	syncRetry      time.Duration
	secretMaxAge   time.Duration
	store          Store
	onLogin        func(context.Context, Identity) (string, error)
	logWarnRows    int // 日志表到这么多行健康检查就提醒扩容

	hub    hubConfig
	outbox *outbox

	// 登录中心：client_id 是本系统编号（SSO_HUB_URL 最后一段），回调是本站的 /auth/center/callback，密钥由中台发。
	center       oauth2.Config
	centerIssuer string

	locks      sync.Map // idHash → *sync.Mutex
	retryAfter sync.Map // idHash → retryNote，集成平台或中台故障时别每个请求都去查
	usedStates sync.Map // state → 过期时间，同一个回调并发到达时只换一次码

	// 停用名单的同步，syncMu 管：攒着还没拉的请求，最近一轮是不是重试用完了还没成，中台上次真正拉到名单的时间。
	syncMu      sync.Mutex
	syncWant    syncWant
	syncWake    chan struct{}
	syncFailing bool
	syncedAt    time.Time

	backchannelQuota *perMinute // 登录中心的退出令牌：整个进程每分钟收几张

	changeQuota *perMinute // 账号表变更提醒：整个进程处理的次数（含处理完再来一遍）
	changedMu   sync.Mutex
	changing    map[string]bool // record_id → 还要不要再处理一遍（正在处理时又来了提醒）

	// 中台那一侧的状态，hubMu 管。调中台都编序号：两个撤权标记记下是第几次调用发现的，只有之后发出的调用回了 200 才能清掉，
	// 撤权前发出、撤权后才回来的成功不算数（也不能拿它放人）。
	hubMu           sync.Mutex
	hubSeq          int64
	credentialBadAt int64    // 钥匙不对、被撤了或地址配错：发现它的那次调用的序号，0 是没有
	revokedAt       int64    // 本应用的账号登录被中台停了：发现它的那次调用的序号，0 是没有
	stateRevision   float64  // 见过的最新系统开关版本：提醒里的版本不比它新就不去拉
	hubProblems     []string // 中台最近一次报的表结构问题（accounts_column:*、log_column:*）
	hubDown         bool     // 最近一次调账号接口是暂时不可用（健康检查 accounts_unreachable）
	centerRefused   bool     // 最近一次找登录中心换码，中心不认本应用的客户端密钥（健康检查 center_credential_invalid）
	logNearFull     bool     // 最近一次读到的日志表总行数到了 logWarnRows

	// SSO 应用密钥，也归 hubMu 管。只有 sso/client 的回包能换掉它、把它作废，并且按调用序号生效：迟到的回包救不回旧密钥。
	secret         ssoSecret     // secret.secret 为空是手上没有
	secretSeq      int64         // 最近一次改动它的 sso/client 调用的序号
	secretWhy      string        // 手上为什么没有（写 stderr 用，变了才写）
	secretRejected string        // 集成平台不认、中台也没有更新的那个版本
	secretRejectAt time.Time     // 最近一次被拒的时间，过了 secretRejectFor 就再试
	secretAskedAt  time.Time     // 最近一次问中台
	secretAsking   chan struct{} // 正在问中台时不为空，问完关掉
	secretAskKind  string        // 最近一次问中台的结果（hubOK、hubUnavailable…）

	// 本进程登录或核对成功过的账号会话（idHash）。被停、钥匙被撤这些标记不落盘，别的进程也看不到：
	// 不在这里的会话（重启前的、别的实例的、从备份恢复的）先找中台核对成功一次，才谈得上不用核对和宽限。
	checkedMu   sync.Mutex
	checkedHere map[string]bool

	// 拉系统开关：同一时间只拉一个，两次之间至少隔 statePullGap；拉的时候又来了提醒，拉完再拉一次。stateMu 管。
	stateMu       sync.Mutex
	statePulling  bool
	stateAgain    bool
	statePulledAt time.Time
}

// hubConfig 是中台给本系统的地址（…/hub-auth/<系统编号>）和 pqk_ 钥匙。
type hubConfig struct{ url, key string }

// retryNote 是一个会话这次核对失败后多久内不再去查，grace 是这期间能不能按宽限放行（只有「暂时不可用」能）；
// unclearSince 是刷新从什么时候起一直回说不清的 4xx（没有就是零值）。
type retryNote struct {
	until        time.Time
	grace        bool
	unclearSince time.Time
}

// New 读环境变量（九个必填）：SSO_ISSUER、SSO_CLIENT_ID、SSO_REDIRECT_URL、SSO_TOKEN_KEY、
// SSO_HUB_URL（中台给本系统的地址，…/hub-auth/<系统编号>）、SSO_HUB_KEY（中台发给本系统的 pqk_ 钥匙）、SSO_OUTBOX_FILE（日志待写队列）、
// SSO_CENTER_ISSUER（登录中心，https://login.fbif.com）、SSO_CENTER_CLIENT_SECRET（中台发的登录中心客户端密钥）。
// 登录中心的 client_id 就是系统编号，回调是 SSO_REDIRECT_URL 同一个域名下的 /auth/center/callback（中心那边登记的也是它）。
// SSO 应用密钥、停用名单都由中台发，env 里不放；可选 SSO_RECONCILE_SECONDS（兜底对账间隔，整数秒，默认 21600，
// 总账本「定时任务和回调」里改，经中台发来）、SSO_LOG_WARN_ROWS（日志表快满提醒线，默认 45000）。retiredEnv 里的变量还在就启动失败。
// SSO_CHECK_INTERVAL、SSO_SYNC_MIN_GAP、SSO_SYNC_RETRY、SSO_SECRET_MAX_AGE 合规测试专用，生产留空。
// 事件优先：账号靠登录、用户的请求和两种账号变更提醒，名单靠名单提醒，日志有东西进来就写；
// 进程里另有一个兜底对账循环（reconcileLoop），启动先跑一轮，之后每 SSO_RECONCILE_SECONDS 一轮。
func New(ctx context.Context, opt Options) (*SSO, error) {
	if opt.Store == nil || opt.OnLogin == nil {
		return nil, errors.New("sso: Store and OnLogin are required")
	}
	var retired []string
	for _, k := range retiredEnv {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			retired = append(retired, k)
		}
	}
	if len(retired) > 0 {
		return nil, fmt.Errorf("sso: delete %s from env: v6 reaches the accounts and log tables, the SSO client secret and the blocklist only through the FBIF hub (SSO_HUB_URL, SSO_HUB_KEY)", strings.Join(retired, ", "))
	}
	env := map[string]string{}
	var missing []string
	for _, k := range []string{"SSO_ISSUER", "SSO_CLIENT_ID", "SSO_REDIRECT_URL", "SSO_TOKEN_KEY", "SSO_HUB_URL", "SSO_HUB_KEY", "SSO_OUTBOX_FILE", "SSO_CENTER_ISSUER", "SSO_CENTER_CLIENT_SECRET"} {
		env[k] = strings.TrimSpace(os.Getenv(k))
		if env[k] == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("sso: missing env %s", strings.Join(missing, ", "))
	}
	redirect, err := url.Parse(env["SSO_REDIRECT_URL"])
	if err != nil || redirect.Path != callbackPath {
		return nil, fmt.Errorf("sso: SSO_REDIRECT_URL must end with %s", callbackPath)
	}
	key, err := base64.StdEncoding.DecodeString(env["SSO_TOKEN_KEY"])
	if err != nil || len(key) != 32 {
		return nil, errors.New("sso: SSO_TOKEN_KEY must be 32 random bytes in base64 (openssl rand -base64 32)")
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)

	checkEvery, err := envDuration("SSO_CHECK_INTERVAL", defaultCheck)
	if err != nil {
		return nil, err
	}
	reconcileEvery, note := reconcileInterval(os.Getenv("SSO_RECONCILE_SECONDS"), strings.TrimSpace(os.Getenv("SSO_CHECK_INTERVAL")) != "")
	log.Printf("sso: reconcile every %ds (SSO_RECONCILE_SECONDS%s)", reconcileEvery/time.Second, note)
	// 每次拉名单都算集成平台一次工作流运行（按月计费）：伪造的提醒再多，一个实例每 30 秒最多拉一次。
	syncMinGap, err := envDuration("SSO_SYNC_MIN_GAP", defaultGap)
	if err != nil {
		return nil, err
	}
	syncRetry, err := envDuration("SSO_SYNC_RETRY", defaultSyncRetry)
	if err != nil {
		return nil, err
	}
	secretMaxAge, err := envDuration("SSO_SECRET_MAX_AGE", defaultSecretAge)
	if err != nil {
		return nil, err
	}
	logWarnRows := defaultLogWarnRows
	if v := strings.TrimSpace(os.Getenv("SSO_LOG_WARN_ROWS")); v != "" {
		if logWarnRows, err = strconv.Atoi(v); err != nil || logWarnRows <= 0 {
			return nil, fmt.Errorf("sso: bad SSO_LOG_WARN_ROWS %q, want a positive row count", v)
		}
	}
	if !hubURLFormat.MatchString(env["SSO_HUB_URL"]) {
		return nil, errors.New("sso: SSO_HUB_URL must be http://127.0.0.1:<port>/hub-auth/<system code> (localhost, [::1] too) or https://<host>/hub-auth/<system code>, without user info")
	}
	if !hubKeyFormat.MatchString(env["SSO_HUB_KEY"]) {
		return nil, errors.New("sso: SSO_HUB_KEY must be the pqk_ key the hub issued for this system")
	}
	if !centerIssuerFormat.MatchString(env["SSO_CENTER_ISSUER"]) {
		return nil, errors.New("sso: SSO_CENTER_ISSUER must be the login center's origin without a path, like https://login.fbif.com")
	}
	if !centerSecretFormat.MatchString(env["SSO_CENTER_CLIENT_SECRET"]) {
		return nil, errors.New("sso: SSO_CENTER_CLIENT_SECRET must be the login center client secret the hub issued for this system")
	}
	ob, err := openOutbox(env["SSO_OUTBOX_FILE"])
	if err != nil {
		return nil, err
	}

	// 签发方地址形如 https://anycross.feishu.cn/sso/FJY632B9P3E，末尾是开这个集成平台的企业。
	issuerCompany := env["SSO_ISSUER"][strings.LastIndex(env["SSO_ISSUER"], "/")+1:]
	if !allowedCompanyCodes[issuerCompany] {
		return nil, errors.New("sso: SSO_ISSUER must end with /sso/<FBIF or 富的 company code>, like https://anycross.feishu.cn/sso/FJY632B9P3E")
	}
	provider, err := oidc.NewProvider(ctx, env["SSO_ISSUER"])
	if err != nil {
		return nil, fmt.Errorf("sso: discovery: %w", err)
	}
	var meta struct {
		Revocation string `json:"revocation_endpoint"`
	}
	if err := provider.Claims(&meta); err != nil || meta.Revocation == "" || provider.UserInfoEndpoint() == "" {
		return nil, errors.New("sso: discovery lacks userinfo or revocation endpoint")
	}
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	// 带着 SSO 密钥、令牌去的接口只认集成平台自己的地址：发现文档被改了也送不出去。
	for _, u := range []string{endpoint.TokenURL, meta.Revocation, provider.UserInfoEndpoint()} {
		if o := origin(u); o == "" || o != origin(env["SSO_ISSUER"]) {
			return nil, errors.New("sso: discovery: token, revocation and userinfo endpoints must be on SSO_ISSUER's origin")
		}
	}

	s := &SSO{
		oauth: oauth2.Config{
			ClientID:    env["SSO_CLIENT_ID"],
			RedirectURL: env["SSO_REDIRECT_URL"],
			Endpoint:    endpoint,
			Scopes:      []string{oidc.ScopeOpenID, "profile", "email", oidc.ScopeOfflineAccess},
		},
		// 集成平台也声明了 HS256，只认 RS256，避免拿 client secret 伪造签名。
		verifier:       provider.Verifier(&oidc.Config{ClientID: env["SSO_CLIENT_ID"], SupportedSigningAlgs: []string{oidc.RS256}}),
		issuerCompany:  issuerCompany,
		userInfoURL:    provider.UserInfoEndpoint(),
		revokeURL:      meta.Revocation,
		aead:           aead,
		checkEvery:     checkEvery,
		reconcileEvery: reconcileEvery,
		syncMinGap:     syncMinGap,
		syncRetry:      syncRetry,
		secretMaxAge:   secretMaxAge,
		store:          opt.Store,
		onLogin:        opt.OnLogin,
		logWarnRows:    logWarnRows,
		hub:            hubConfig{url: env["SSO_HUB_URL"], key: env["SSO_HUB_KEY"]},
		outbox:         ob,
		// 中心的地址是我们自己定的，不读发现文档：项目启动不依赖中心在不在。
		center: oauth2.Config{
			ClientID:     env["SSO_HUB_URL"][strings.LastIndex(env["SSO_HUB_URL"], "/")+1:],
			ClientSecret: env["SSO_CENTER_CLIENT_SECRET"],
			RedirectURL:  redirect.Scheme + "://" + redirect.Host + centerCallbackPath, // 和中心登记的一字不差：不补默认端口
			Endpoint:     oauth2.Endpoint{AuthURL: env["SSO_CENTER_ISSUER"] + "/authorize", TokenURL: env["SSO_CENTER_ISSUER"] + "/oauth/token", AuthStyle: oauth2.AuthStyleInHeader},
			Scopes:       []string{oidc.ScopeOpenID},
		},
		centerIssuer: env["SSO_CENTER_ISSUER"],
		syncWake:     make(chan struct{}, 1),
		changeQuota:  newPerMinute(accountsChangedPerMinute),
		changing:     map[string]bool{},
		checkedHere:  map[string]bool{},

		backchannelQuota: newPerMinute(backchannelPerMinute),
	}
	go s.syncLoop()
	go ob.saveLoop()
	go s.outboxWorker()
	go s.reconcileLoop()
	return s, nil
}

// Register 挂十一条路由：去登录中心、中心回调、中心登录页上的飞书按钮、飞书登录、飞书回调、退出、登录中心的退出通知、/api/me、
// 名单提醒、账号变更提醒、健康检查。
// 要记请求日志，再用 Middleware 包住整个 mux。
func (s *SSO) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+centerLoginPath, s.handleCenterLogin)
	mux.HandleFunc("GET "+centerCallbackPath, s.handleCenterCallback)
	mux.HandleFunc("GET "+centerFeishuPath, s.handleCenterFeishu)
	mux.HandleFunc("GET "+loginPath, s.handleLogin)
	mux.HandleFunc("GET "+callbackPath, s.handleCallback)
	mux.HandleFunc("POST "+logoutPath, s.handleLogout)
	mux.HandleFunc("POST "+backchannelPath, s.handleBackchannelLogout)
	mux.Handle("GET "+mePath, s.RequireUser(http.HandlerFunc(s.handleMe)))
	mux.HandleFunc("POST "+blocklistPath, s.handleBlocklistChanged)
	mux.HandleFunc("POST "+accountsChangedPath, s.handleAccountsChanged)
	mux.HandleFunc("GET "+healthPath, s.handleHealth)
}

// Shutdown 在进程退出前调（http.Server.Shutdown 之后）：把待写队列里还没落盘的日志落盘，重启后接着写。
func (s *SSO) Shutdown(ctx context.Context) error {
	return s.outbox.flush(ctx)
}

// RequireUser 包住所有要登录的接口，包括下载、导出、长连接。没登录回 401。
func (s *SSO) RequireUser(next http.Handler) http.Handler { return s.require(next, false) }

// RequireFreshUser 给 HR、盖章、改权限、敏感导出：每次当场找集成平台核对，核对不了就拒绝；账号密码会话一律 403。
func (s *SSO) RequireFreshUser(next http.Handler) http.Handler { return s.require(next, true) }

// RequirePage 包住发页面的处理（index.html、前端路由回退），不包公开页面和静态资源：浏览器打开页面时没登录，当场 302 去登录、
// 登完回到这一页。页面不先发下来，就不会先闪一下「未登录」再跳（同阿里云、火山引擎的控制台）。飞书客户端里直接走飞书登录、不用点。
// 只拦 GET、HEAD；会话一时查不了（存储、名单出错）照常发页面，由页面上的接口报错。
func (s *SSO) RequirePage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if sess, status := s.authenticate(r.Context(), r, false); sess == nil && status == http.StatusUnauthorized {
				setCookie(w, sessionCookie, "", -1)
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, pageLoginURL(r), http.StatusFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

var feishuClientUA = regexp.MustCompile(`(?i)Lark|Feishu`)

// pageLoginURL 是没登录打开页面时去的地方：飞书客户端里去飞书登录（端内免登），别处去登录中心；都带上原来要去的页面。
func pageLoginURL(r *http.Request) string {
	start := centerLoginPath
	if feishuClientUA.MatchString(r.UserAgent()) {
		start = loginPath
	}
	if next := safeNext(r.URL.RequestURI()); next != "" {
		return start + "?next=" + url.QueryEscape(next)
	}
	return start
}

type userKey struct{}

func CurrentUser(r *http.Request) *User {
	u, _ := r.Context().Value(userKey{}).(*User)
	return u
}

// ---------- 登录 ----------

type loginTxn struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Next     string `json:"next"`
	Expires  int64  `json:"expires"`
	SID      string `json:"sid,omitempty"` // 登录中心给的浏览器会话编号（验过的 sid_hint），登成了记在会话上
}

// handleCenterLogin 是没登录时去的地方（前端、/api/me 回的 login_url 都指这里）：建一个登录事务，跳到登录中心的登录页。
// ?next= 是登录完回哪（只认本站路径）；?error= 是要登录页显示的出错提示，只转 loginHints 里的。
func (s *SSO) handleCenterLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t := loginTxn{State: randToken(), Nonce: randToken(), Verifier: oauth2.GenerateVerifier(), Next: safeNext(q.Get("next")), Expires: time.Now().Add(txnTTL).Unix()}
	raw, _ := json.Marshal(t)
	// 和飞书那条一样绑定发起登录的浏览器；用途不同（center-txn），两条路的事务不能互换。
	setCookie(w, txnCookieName(t.State), s.seal("center-txn", raw), txnTTL)
	opts := []oauth2.AuthCodeOption{oidc.Nonce(t.Nonce), oauth2.S256ChallengeOption(t.Verifier)}
	if hint := q.Get("error"); loginHints[hint] {
		opts = append(opts, oauth2.SetAuthURLParam("fbif_error", hint))
	}
	// 刚在本项目退出：要中心出登录页，别自动走飞书又登回来。
	if c, err := r.Cookie(signedOutCookie); err == nil && c.Value != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"))
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.center.AuthCodeURL(t.State, opts...), http.StatusFound)
}

// centerTxn 取出这个浏览器里 state 对应的登录中心事务；对不上、过期返回 false。
func (s *SSO) centerTxn(r *http.Request, state string) (loginTxn, bool) {
	var t loginTxn
	c, err := r.Cookie(txnCookieName(state))
	if err != nil || len(state) < 16 {
		return t, false
	}
	raw, err := s.open("center-txn", c.Value)
	if err != nil || json.Unmarshal(raw, &t) != nil || t.State != state || time.Now().Unix() > t.Expires {
		return loginTxn{}, false
	}
	return t, true
}

// handleCenterFeishu 是登录中心登录页上的「使用飞书登录」，也是这个浏览器用飞书登过时中心直接送回来的地方：
// 按 state 找回这次登录要回的页面，走本项目原来的飞书登录。找不到（换了浏览器、放太久）就登完回首页：和直接打开飞书登录一样，不多放任何东西。
// 带着 sid_hint（中心签的这个浏览器的会话编号）且验得过（签名、typ、发证方、受众是本系统、没过期、state 对得上），就记进飞书登录事务，
// 登成了记在会话上（一处退出、处处退出）；验不过只记 stderr，照常登，只是这个会话退出只退本项目。
// 中心的事务留着：飞书那边退回来，还能在同一个登录页用账号密码登。
func (s *SSO) handleCenterFeishu(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t, ok := s.centerTxn(r, q.Get("state"))
	sid := ""
	if hint := q.Get("sid_hint"); hint != "" && ok {
		ctx, cancel := context.WithTimeout(r.Context(), centerLogoutTimeout)
		c, err := s.centerToken(ctx, hint, "fbif-sid+jwt")
		cancel()
		switch {
		case err != nil:
			log.Printf("sso: sid_hint refused: %v", err)
		case c.State != t.State || !sidFormat.MatchString(c.SID):
			log.Printf("sso: sid_hint refused: state or sid does not match")
		default:
			sid = c.SID
		}
	}
	s.startFeishuWith(w, r, t.Next, sid)
}

// handleCenterCallback 是账号密码在登录中心登对了之后回来的地方：核对事务、拿客户端密钥和 PKCE 换码、验 id_token，建账号会话。
// 中心刚经中台核过密码、开没开通本应用，这里不再问中台；本进程已知本应用被停、中台不认本应用的钥匙时照样不放。
func (s *SSO) handleCenterCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ev := requestEvent(r, methodAccount)
	fail := func(code, reason string, why string, args ...any) {
		log.Printf("sso: account login failed (%s): "+why, append([]any{code}, args...)...)
		ev.Event, ev.Reason = eventLoginFail, reason
		s.record(ev)
		http.Redirect(w, r, centerLoginPath+"?error="+code, http.StatusFound)
	}
	state := q.Get("state")
	if len(state) < 16 {
		fail("expired", "回调没带 state", "no state")
		return
	}
	t, ok := s.centerTxn(r, state)
	setCookie(w, txnCookieName(state), "", -1)
	if !ok {
		fail("expired", "登录事务不在这个浏览器里或已过期", "center txn missing or invalid")
		return
	}
	ev.Next = t.Next
	if !s.consumeState(state, t.Expires) {
		fail("expired", "登录事务已用过", "center txn already used")
		return
	}
	if e := q.Get("error"); e != "" {
		fail("failed", "登录中心返回错误："+cleanCode(e), "center error %s", cleanCode(e))
		return
	}
	code := q.Get("code")
	if code == "" {
		fail("failed", "回调没带授权码", "no code")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), upstreamLimit)
	defer cancel()
	tok, err := s.center.Exchange(ctx, code, oauth2.VerifierOption(t.Verifier))
	kind, why := tokenTrouble(err)
	s.hubMu.Lock()
	s.centerRefused = kind == tokenSecret || (s.centerRefused && kind != "")
	s.hubMu.Unlock()
	switch kind {
	case "":
	case tokenSecret:
		fail("unavailable", "登录中心不认本应用的客户端密钥", "center exchange: %s", why)
		return
	case tokenUnavailable:
		fail("unavailable", "登录中心暂时不可用", "center exchange: %s", why)
		return
	default:
		fail("failed", "找登录中心换令牌失败", "center exchange: %s", why)
		return
	}
	acct, err := s.verifyCenterLogin(ctx, tok, t.Nonce)
	if err != nil {
		fail("failed", "登录中心的 ID 令牌无效", "%v", err)
		return
	}
	id := Identity{CompanyCode: accountCompanyCode, Sub: acct.recordID, Name: acct.name, Apps: acct.apps}
	ev = withIdentity(ev, id)
	ev.RecordID, ev.Account = acct.recordID, acct.name
	refuse := func(sessionHash string) bool {
		revoked, credentialBad := s.hubFlags()
		if !revoked && !credentialBad {
			return false
		}
		if sessionHash != "" {
			s.endSession(ctx, sessionHash)
		}
		if revoked {
			fail("disabled", revokedReason, "account login stopped for this app")
		} else {
			fail("unavailable", "中台不认本应用的钥匙", "hub refuses SSO_HUB_KEY")
		}
		return true
	}
	// 本进程记着本应用被停、或中台不认本应用的钥匙：当场拉一次开关核实（恢复了标记就清掉），还在才不放。
	if revoked, credentialBad := s.hubFlags(); revoked || credentialBad {
		s.pullState()
	}
	if refuse("") {
		return
	}
	userID, err := s.onLogin(ctx, id)
	if errors.Is(err, ErrNotAllowed) {
		fail("unauthorized_tenant", "本项目不让进", "project refused %s", id.Key())
		return
	} else if err != nil {
		fail("failed", "OnLogin 出错", "OnLogin %s: %v", id.Key(), err)
		return
	}
	sid := randToken()
	now := time.Now()
	// 核对周期从中台核密码那一刻算起。
	checkedAt := time.UnixMilli(int64(acct.verifiedAt))
	if checkedAt.After(now) {
		checkedAt = now
	}
	sess := Session{IDHash: hashID(sid), UserID: userID, Identity: id, CreatedAt: now, CheckedAt: checkedAt, LastSeenAt: now, Version: 1,
		Account: true, AccountCredential: acct.credential, Gen: sessionGen}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		fail("failed", "建会话失败", "create session: %v", err)
		return
	}
	// 建会话期间被停了或钥匙被撤了：撤掉刚建的会话（下线全部账号会话那一遍可能早于它）。
	if refuse(sess.IDHash) {
		return
	}
	s.markChecked(sess.IDHash)
	// 登录那一刻本项目不问中台，「中台暂时不可用」的标记不会跟着刷新：还记着它，就在后台找中台核对一次这个人，中台好了标记就清掉。
	s.hubMu.Lock()
	hubDown := s.hubDown
	s.hubMu.Unlock()
	if hubDown {
		go func() {
			s.settleAccountSession(context.Background(), nil, sess, s.accountCall("/account/check", map[string]any{"record_id": sess.Identity.Sub}))
		}()
	}
	setCookie(w, sessionCookie, sid, sessionIdle+touchEvery)
	setCookie(w, signedOutCookie, "", -1)
	log.Printf("sso: login %s user=%s", id.Key(), userID)
	ev.Event, ev.Session = eventLoginOK, sessionTag(sess.IDHash)
	s.record(ev)
	next := t.Next
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// centerHTTP 找登录中心取公钥不跟跳转。
var centerHTTP = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// centerVerifier 验登录中心签的令牌（id_token、sid_hint、退出令牌）：签名（RS256）、发证方是中心、受众是本系统、没过期。
// 公钥每次现取：go-oidc 的 RemoteKeySet 对见过的钥匙一直缓存，中心撤掉的那把要马上不认。
func (s *SSO) centerVerifier(ctx context.Context) *oidc.IDTokenVerifier {
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(ctx, centerHTTP), s.centerIssuer+"/keys")
	return oidc.NewVerifier(s.centerIssuer, keys, &oidc.Config{ClientID: s.center.ClientID, SupportedSigningAlgs: []string{oidc.RS256}})
}

// verifyCenterLogin 验登录中心的 id_token：签名（RS256）、发证方、受众是本系统、没过期、nonce 对得上，
// 再读中台核密码时给的身份：sub 是账号表那一行的记录 ID，fbif_credential 是凭据版本，fbif_verified_at 是中台核验时间（毫秒）。
func (s *SSO) verifyCenterLogin(ctx context.Context, tok *oauth2.Token, nonce string) (hubAccount, error) {
	rawID, _ := tok.Extra("id_token").(string)
	idt, err := s.centerVerifier(ctx).Verify(ctx, rawID)
	if err != nil {
		return hubAccount{}, fmt.Errorf("center id_token: %w", err)
	}
	if idt.Nonce != nonce {
		return hubAccount{}, errors.New("center id_token nonce mismatch")
	}
	var c struct {
		Name       string   `json:"name"`
		Apps       []string `json:"fbif_apps"`
		Credential string   `json:"fbif_credential"`
		VerifiedAt int64    `json:"fbif_verified_at"`
	}
	if err := idt.Claims(&c); err != nil || !recordIDFormat.MatchString(idt.Subject) || !credentialFormat.MatchString(c.Credential) || c.Name == "" {
		return hubAccount{}, errors.New("center id_token: sub, name or fbif_credential missing or malformed")
	}
	apps := []string{}
	for _, app := range c.Apps {
		if !recordIDFormat.MatchString(app) {
			return hubAccount{}, errors.New("center id_token: malformed fbif_apps")
		}
		apps = append(apps, app)
	}
	now := time.Now()
	if at := time.UnixMilli(c.VerifiedAt); at.Before(now.Add(-centerVerifiedBefore)) || at.After(now.Add(centerVerifiedAfter)) {
		return hubAccount{}, errors.New("center id_token: fbif_verified_at too old or in the future")
	}
	return hubAccount{recordID: idt.Subject, name: clip(c.Name, 100), apps: apps, credential: c.Credential, verifiedAt: float64(c.VerifiedAt)}, nil
}

// handleLogin 是飞书客户端里的飞书登录（/auth/feishu/fbif/login?next=…，端内免登）：项目自己的 SSO 应用，直连集成平台。
// 浏览器里打开的转去登录中心：浏览器里的飞书登录都要挂上登录中心，别的项目退出才管得到（2026-11-11）。
func (s *SSO) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if !feishuClientUA.MatchString(r.UserAgent()) {
		to := centerLoginPath
		if next != "" {
			to += "?next=" + url.QueryEscape(next)
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, to, http.StatusFound)
		return
	}
	s.startFeishu(w, r, next)
}

func (s *SSO) startFeishu(w http.ResponseWriter, r *http.Request, next string) {
	s.startFeishuWith(w, r, next, "")
}

// startFeishuWith 建飞书登录事务、跳集成平台；sid 是登录中心给的浏览器会话编号（没有就空着）。
func (s *SSO) startFeishuWith(w http.ResponseWriter, r *http.Request, next, sid string) {
	// 手上没有能用的 SSO 密钥就不送去扫码：扫完了也换不了令牌。
	if _, ok := s.secretFor(r.Context()); !ok {
		http.Redirect(w, r, centerLoginPath+"?error=unavailable", http.StatusFound)
		return
	}
	t := loginTxn{
		State:    randToken(),
		Nonce:    randToken(),
		Verifier: oauth2.GenerateVerifier(),
		Next:     next,
		Expires:  time.Now().Add(txnTTL).Unix(),
		SID:      sid,
	}
	raw, _ := json.Marshal(t)
	// 登录事务加密放在发起登录的浏览器里：回调换了浏览器就对不上，防止把别人登进自己的账号。
	setCookie(w, txnCookieName(t.State), s.seal("login-txn", raw), txnTTL)
	http.Redirect(w, r, s.oauth.AuthCodeURL(t.State, oidc.Nonce(t.Nonce), oauth2.S256ChallengeOption(t.Verifier)), http.StatusFound)
}

func (s *SSO) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ev := requestEvent(r, methodFeishu)
	fail := func(code, reason string, why string, args ...any) {
		log.Printf("sso: login failed (%s): "+why, append([]any{code}, args...)...)
		ev.Event, ev.Reason = eventLoginFail, reason
		s.record(ev)
		http.Redirect(w, r, centerLoginPath+"?error="+code, http.StatusFound)
	}
	state := q.Get("state")
	if len(state) < 16 {
		fail("expired", "回调没带 state", "no state")
		return
	}
	c, cookieErr := r.Cookie(txnCookieName(state))
	setCookie(w, txnCookieName(state), "", -1)
	if e := q.Get("error"); e != "" {
		fail("denied", "集成平台返回错误："+cleanCode(e), "idp error %s", cleanCode(e))
		return
	}
	var t loginTxn
	if cookieErr != nil {
		fail("expired", "登录事务不在这个浏览器里", "no login txn cookie")
		return
	}
	raw, err := s.open("login-txn", c.Value)
	if err != nil || json.Unmarshal(raw, &t) != nil || t.State != state || time.Now().Unix() > t.Expires {
		fail("expired", "登录事务对不上或已过期", "login txn invalid")
		return
	}
	ev.Next = t.Next
	// shortcut: 只防同一实例内的并发回调；多实例要防，就把登录事务放进共享库原子消费。
	if !s.consumeState(state, t.Expires) {
		fail("expired", "登录事务已用过", "login txn already used")
		return
	}

	code := q.Get("code")
	if code == "" {
		fail("failed", "回调没带授权码", "no code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), upstreamLimit)
	defer cancel()
	sec, ok := s.secretFor(ctx)
	if !ok {
		fail("unavailable", "没有能用的 SSO 密钥", "no usable SSO client secret")
		return
	}
	// 授权码只换一次，失败也不重试：集成平台见到重放会把第一次发的令牌一并作废。本应用密钥不对时也一样，重取只为下一个人。
	tok, err := s.oauthWith(sec).Exchange(ctx, code, oauth2.VerifierOption(t.Verifier))
	if err != nil {
		switch kind, why := tokenTrouble(err); kind {
		case tokenSecret:
			s.secretRefused(ctx, sec)
			fail("unavailable", "集成平台不认本应用的 SSO 密钥", "exchange: %s", why)
		case tokenUnavailable:
			fail("unavailable", "集成平台暂时不可用", "exchange: %s", why)
		default:
			fail("failed", "换令牌失败", "exchange: %s", why)
		}
		return
	}
	id, code, reason, err := s.verifyLogin(ctx, tok, t.Nonce)
	ev = withIdentity(ev, id)
	if err != nil {
		s.revoke(ctx, tok.RefreshToken)
		fail(code, reason, "%v", err)
		return
	}
	if why, blocked := s.blocked(ctx, id.Sub); blocked {
		s.revoke(ctx, tok.RefreshToken)
		fail("disabled", "停用名单："+why, "sub %s refused: %s", id.Sub, why)
		return
	}
	userID, err := s.onLogin(ctx, id)
	if err != nil {
		s.revoke(ctx, tok.RefreshToken)
		if errors.Is(err, ErrNotAllowed) {
			fail("unauthorized_tenant", "本项目不让进", "project refused %s", id.Key())
		} else {
			fail("failed", "OnLogin 出错", "OnLogin: %v", err)
		}
		return
	}

	sid := randToken()
	now := time.Now()
	sess := Session{IDHash: hashID(sid), UserID: userID, Identity: id, RefreshSeal: s.seal("refresh-token", []byte(tok.RefreshToken)), CreatedAt: now, CheckedAt: now, LastSeenAt: now, Version: 1, SID: t.SID, Gen: sessionGen}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		s.revoke(ctx, tok.RefreshToken)
		fail("failed", "建会话失败", "create session: %v", err)
		return
	}
	ev.Session = sessionTag(sess.IDHash)
	// 名单同步先落名单再扫会话；这里先建会话再查一次名单，两边交错时不会漏掉人。
	if why, blocked := s.blocked(ctx, id.Sub); blocked {
		s.endSession(ctx, sess.IDHash)
		fail("disabled", "停用名单："+why, "sub %s blocked during login", id.Sub)
		return
	}
	setCookie(w, sessionCookie, sid, sessionIdle+touchEvery)
	setCookie(w, signedOutCookie, "", -1)
	log.Printf("sso: login %s user=%s", id.Key(), userID)
	ev.Event = eventLoginOK
	s.record(ev)
	next := t.Next
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// verifyLogin 返回身份；失败时给回调错误码和写进日志的原因。
func (s *SSO) verifyLogin(ctx context.Context, tok *oauth2.Token, nonce string) (Identity, string, string, error) {
	rawID, _ := tok.Extra("id_token").(string)
	idt, err := s.verifier.Verify(ctx, rawID)
	if err != nil {
		return Identity{}, "failed", "ID 令牌无效", fmt.Errorf("id_token: %w", err)
	}
	if idt.Nonce != nonce {
		return Identity{}, "failed", "ID 令牌的 nonce 对不上", errors.New("id_token nonce mismatch")
	}
	var claims Identity
	if err := idt.Claims(&claims); err != nil || claims.Sub == "" {
		return Identity{}, "failed", "ID 令牌没有 sub", errors.New("id_token without sub")
	}
	// Apps 只给账号密码会话：飞书登录的人不受限，令牌里万一带了同名字段也不认。
	claims.Apps = nil
	// 没开「关联组织访问」的应用，令牌里没有 company_code，能登进来的只有签发方企业自己的人：按签发方认。
	// 2026-11-10 缺了就拒，把合同盖章、HR 的 FBIF 自己人全挡在门外。写了但不是 FBIF/富的，下面照拒。
	if claims.CompanyCode == "" {
		claims.CompanyCode = s.issuerCompany
		log.Printf("sso: id_token has no company_code, using the issuer's %s for sub %s", s.issuerCompany, claims.Sub)
	}
	// 下面几种失败把 claims 一并返回，日志里能看出是谁被拒。
	if !allowedCompanyCodes[claims.CompanyCode] {
		return claims, "unauthorized_tenant", "不是 FBIF 或富的：" + cleanCode(claims.CompanyCode), fmt.Errorf("company_code %q not allowed", claims.CompanyCode)
	}
	if tok.RefreshToken == "" {
		return claims, "failed", "没拿到 refresh_token", errors.New("no refresh_token (offline_access not granted)")
	}
	info, err := s.userInfo(ctx, tok.AccessToken, claims.Sub)
	if errors.Is(err, errDenied) {
		return claims, "denied", "集成平台禁止访问", err
	}
	if err != nil {
		return claims, "failed", "查用户信息失败", err
	}
	if claims.Name == "" {
		claims.Name = info.Name
	}
	if claims.Email == "" {
		claims.Email = info.Email
	}
	if claims.Picture == "" {
		claims.Picture = info.Picture
	}
	return claims, "", "", nil
}

// ---------- 会话 ----------

var (
	errRejected    = errors.New("sso: session rejected")
	errUnavailable = errors.New("sso: idp unavailable")
	errStore       = errors.New("sso: store unavailable")
	errDenied      = errors.New("sso: idp denied this user")
	errTokenStale  = errors.New("sso: access token superseded")
	errSecret      = errors.New("sso: no usable SSO client secret")
	errUnclear     = errors.New("sso: idp refused, reason unclear")
)

func (s *SSO) require(next http.Handler, fresh bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		note := noteOf(r)
		if note != nil && fresh {
			note.mu.Lock()
			note.fresh = true
			note.mu.Unlock()
		}
		sess, status := s.authenticate(r.Context(), r, fresh)
		if sess == nil {
			if status == http.StatusUnauthorized {
				setCookie(w, sessionCookie, "", -1)
				writeJSON(w, status, map[string]any{"error": "login_required", "login_url": centerLoginPath})
			} else if status == http.StatusForbidden {
				writeJSON(w, status, map[string]any{"error": "sso_required"})
			} else {
				writeJSON(w, status, map[string]any{"error": "login_service_unavailable"})
			}
			return
		}
		if time.Since(sess.LastSeenAt) >= touchEvery {
			touched := *sess
			touched.LastSeenAt = time.Now()
			if s.store.UpdateSession(r.Context(), touched, sess.Version) == nil {
				if c, err := r.Cookie(sessionCookie); err == nil {
					setCookie(w, sessionCookie, c.Value, sessionIdle+touchEvery)
				}
			}
		}
		if note != nil {
			note.mu.Lock()
			note.sess = sess
			note.mu.Unlock()
		}
		ctx := context.WithValue(r.Context(), userKey{}, &User{ID: sess.UserID, Identity: sess.Identity})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *SSO) authenticate(ctx context.Context, r *http.Request, fresh bool) (*Session, int) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, http.StatusUnauthorized
	}
	sess, err := s.store.GetSession(ctx, hashID(c.Value))
	if err != nil {
		log.Printf("sso: load session: %v", err)
		return nil, http.StatusServiceUnavailable
	}
	if sess == nil {
		return nil, http.StatusUnauthorized
	}
	// 滑动续期的时间每小时才写一次，多留一小时，保证 30 天没用才过期。
	if time.Since(sess.LastSeenAt) > sessionIdle+touchEvery {
		s.endSession(ctx, sess.IDHash)
		return nil, http.StatusUnauthorized
	}
	if staleBrowserSession(sess, r) {
		s.revokeSession(ctx, r, sess.IDHash, "一处登录处处登录上线前的浏览器会话，请重新登录")
		return nil, http.StatusUnauthorized
	}
	if sess.Account {
		// 敏感接口只认集成平台身份：账号密码没法当场找集成平台核对。
		if fresh {
			return nil, http.StatusForbidden
		}
		if s.accountFresh(sess) {
			return sess, http.StatusOK
		}
		return s.checkAccount(ctx, r, sess)
	}
	// 每个请求都查名单：名单落库后、踢人前进程挂了，旧会话也进不来。
	if e, err := s.store.BlockedEntry(ctx, sess.Identity.Sub); err != nil {
		log.Printf("sso: load blocklist: %v", err)
		return nil, http.StatusServiceUnavailable
	} else if e != nil {
		s.revokeSession(ctx, r, sess.IDHash, "停用名单："+e.Reason)
		return nil, http.StatusUnauthorized
	}
	if !fresh && time.Since(sess.CheckedAt) < s.checkEvery {
		return sess, http.StatusOK
	}
	// 手上没有能用的 SSO 密钥：回 503，不宽限也不删会话，和中台钥匙不对一样。
	if _, ok := s.secretFor(ctx); !ok {
		return nil, http.StatusServiceUnavailable
	}
	inGrace := !fresh && time.Since(sess.CheckedAt) < s.checkEvery+outageGrace
	if n, ok := s.retryAfter.Load(sess.IDHash); ok && !fresh && time.Now().Before(n.(retryNote).until) {
		if inGrace && n.(retryNote).grace {
			return sess, http.StatusOK
		}
		return nil, http.StatusServiceUnavailable
	}
	// 刷新不跟着这个请求取消：浏览器刷新、关页面时请求被取消，集成平台可能已经换了新的 refresh_token、
	// 旧的作废，回包却没人收，这个会话就再也刷新不了（2026-11-10 HR 生产，之后一直回 400 invalid_request）。
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), upstreamLimit)
	defer cancel()
	checked, err := s.check(cctx, r, sess.IDHash, fresh)
	switch {
	case err == nil:
		s.retryAfter.Delete(sess.IDHash)
		return checked, http.StatusOK
	case errors.Is(err, errUnclear):
		// 说不清的拒绝先回 503 等它好；从第一次起过了一个核对周期还是这样，就当被拒：下线、回 401，让人重新登录，
		// 不能一直 503、怎么刷新都进不来。
		since := time.Now()
		if n, ok := s.retryAfter.Load(sess.IDHash); ok && !n.(retryNote).unclearSince.IsZero() {
			since = n.(retryNote).unclearSince
		}
		if time.Since(since) >= s.checkEvery {
			s.revokeSession(cctx, r, sess.IDHash, "集成平台一直拒绝刷新（原因说不清）")
			return nil, http.StatusUnauthorized
		}
		s.retryAfter.Store(sess.IDHash, retryNote{until: time.Now().Add(outageBackoff), unclearSince: since})
		return nil, http.StatusServiceUnavailable
	case errors.Is(err, errUnavailable):
		s.retryAfter.Store(sess.IDHash, retryNote{until: time.Now().Add(outageBackoff), grace: true})
		if !inGrace {
			return nil, http.StatusServiceUnavailable
		}
		// 核对期间可能已被踢：放行前以库里为准。
		again, gerr := s.store.GetSession(ctx, sess.IDHash)
		if gerr != nil {
			return nil, http.StatusServiceUnavailable
		}
		if again == nil {
			return nil, http.StatusUnauthorized
		}
		return again, http.StatusOK
	case errors.Is(err, errStore), errors.Is(err, errSecret):
		return nil, http.StatusServiceUnavailable
	default:
		return nil, http.StatusUnauthorized
	}
}

// check 用 refresh_token 换新的 access_token，再调用户信息：集成平台的「禁止访问」拦在用户信息接口，
// 刷新本身照样成功，所以只看刷新成不成功没有用。
func (s *SSO) check(ctx context.Context, r *http.Request, idHash string, fresh bool) (*Session, error) {
	mu, _ := s.locks.LoadOrStore(idHash, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	cur, err := s.store.GetSession(ctx, idHash)
	if err != nil {
		return nil, errStore
	}
	if cur == nil {
		return nil, errRejected
	}
	if !fresh && time.Since(cur.CheckedAt) < s.checkEvery {
		return cur, nil
	}
	rt, err := s.open("refresh-token", cur.RefreshSeal)
	if err != nil {
		s.revokeSession(ctx, r, idHash, "会话里的令牌解不开")
		return nil, errRejected
	}
	sec, ok := s.secretFor(ctx)
	if !ok {
		return nil, errSecret
	}
	refresh := func() (*oauth2.Token, error) {
		return s.oauthWith(sec).TokenSource(ctx, &oauth2.Token{RefreshToken: string(rt), Expiry: time.Now().Add(-time.Minute)}).Token()
	}
	nt, err := refresh()
	kind, why := tokenTrouble(err)
	// 本应用密钥没通过时 refresh_token 没被用掉：中台给了新版本就用它重试一次。
	if kind == tokenSecret {
		if sec, ok = s.secretRefused(ctx, sec); ok {
			nt, err = refresh()
			if kind, why = tokenTrouble(err); kind == tokenSecret {
				s.secretRefused(ctx, sec)
			}
		}
	}
	switch kind {
	case tokenRefused:
		log.Printf("sso: refresh rejected for %s: %s", cur.Identity.Key(), why)
		if gone, _ := s.store.DeleteSession(ctx, idHash); gone != nil {
			s.recordRevoked(r, gone, "集成平台拒绝刷新")
		}
		s.locks.Delete(idHash)
		return nil, errRejected
	case tokenSecret:
		log.Printf("sso: refresh: %s, the IdP does not accept the SSO client secret", why)
		return nil, errSecret
	case tokenUnclear:
		log.Printf("sso: refresh for %s: %s, keeping the session", cur.Identity.Key(), why)
		return nil, errUnclear
	case tokenUnavailable:
		log.Printf("sso: refresh unavailable: %s", why)
		return nil, errUnavailable
	}
	newRT := nt.RefreshToken
	if newRT == "" {
		newRT = string(rt)
	}
	next := *cur
	next.RefreshSeal = s.seal("refresh-token", []byte(newRT))

	_, uiErr := s.userInfo(ctx, nt.AccessToken, cur.Identity.Sub)
	if errors.Is(uiErr, errDenied) {
		log.Printf("sso: %s denied by idp, ending session", cur.Identity.Key())
		if gone, _ := s.store.DeleteSession(ctx, idHash); gone != nil {
			s.recordRevoked(r, gone, "集成平台禁止访问")
		}
		s.locks.Delete(idHash)
		s.revoke(ctx, newRT)
		return nil, errRejected
	}
	if uiErr != nil {
		saveErr := s.store.UpdateSession(ctx, next, cur.Version)
		// 401 多半是别的实例同时刷新、把这个 access_token 顶掉了（刷新后旧的立即 401）：以库里为准，不踢人。
		if errors.Is(uiErr, errTokenStale) && errors.Is(saveErr, ErrConflict) {
			if again, gerr := s.store.GetSession(ctx, idHash); gerr == nil && again != nil {
				return again, nil
			}
		}
		log.Printf("sso: userinfo unavailable: %v", uiErr)
		return nil, errUnavailable
	}
	next.CheckedAt = time.Now()
	if err := s.store.UpdateSession(ctx, next, cur.Version); err != nil {
		// 这段时间里会话被踢了（或别的实例刚核对过）：以库里为准，晚到的刷新结果不能把会话救回来。
		again, gerr := s.store.GetSession(ctx, idHash)
		if gerr != nil {
			return nil, errStore
		}
		if again == nil {
			s.revoke(ctx, newRT)
			return nil, errRejected
		}
		return again, nil
	}
	next.Version = cur.Version + 1
	return &next, nil
}

// handleMe 回当前用户；账号密码会话多带 apps（能用的应用的 record_id），飞书登录的人不带，表示不受限。
func (s *SSO) handleMe(w http.ResponseWriter, r *http.Request) {
	u := CurrentUser(r)
	me := map[string]any{"id": u.ID, "name": u.Name, "avatar_url": u.Picture, "company_code": u.CompanyCode}
	if u.CompanyCode == accountCompanyCode {
		me["apps"] = append([]string{}, u.Apps...)
	}
	writeJSON(w, http.StatusOK, me)
}

// handleLogout 退出这个项目在这个浏览器里的会话。会话带 SID（浏览器里经登录中心飞书登录的）就告诉登录中心，
// 中心再通知其他项目删掉同一个 SID 的会话（一处退出、处处退出）；账号密码会话、飞书客户端里登的只退本项目。飞书本身不受影响。
func (s *SSO) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		ctx, cancel := context.WithTimeout(r.Context(), upstreamLimit)
		gone, err := s.endSession(ctx, hashID(c.Value))
		cancel()
		if err != nil {
			log.Printf("sso: logout: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "logout_failed"})
			return
		}
		if gone != nil {
			s.record(s.sessionEvent(requestEvent(r, ""), gone, eventLogout, ""))
			if gone.SID != "" {
				s.tellCenterLogout(gone.SID)
			}
		}
	}
	setCookie(w, sessionCookie, "", -1)
	setCookie(w, signedOutCookie, "1", signedOutTTL)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// tellCenterLogout 在后台告诉登录中心这个浏览器的飞书会话退出了（POST /api/logout，HTTP Basic 用中心客户端密钥，表单 sid）。
// 连不上、429、5xx 退避重试，1 小时还不成就放弃；中心回别的 4xx（不认本应用的客户端密钥、sid 不对）不重试。都只记 stderr。
// shortcut: 只在内存里重试，进程重启就丢（其他项目的会话留到各自退出或过期）；要必达，就把它也放进持久待写队列。
func (s *SSO) tellCenterLogout(sid string) {
	go func() {
		start, gap := time.Now(), centerLogoutFirstGap
		for {
			retry, why := s.centerLogout(sid)
			if !retry {
				if why != "" {
					log.Printf("sso: tell login center about logout: %s", why)
				}
				return
			}
			if time.Since(start) >= centerLogoutGiveUp {
				log.Printf("sso: tell login center about logout: gave up after %s: %s", centerLogoutGiveUp, why)
				return
			}
			log.Printf("sso: tell login center about logout: %s, retry in %s", why, gap)
			time.Sleep(gap)
			gap = min(gap*2, centerLogoutMaxGap)
		}
	}()
}

func (s *SSO) centerLogout(sid string) (retry bool, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), centerLogoutTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.centerIssuer+centerLogoutPath, strings.NewReader(url.Values{"sid": {sid}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.center.ClientID, s.center.ClientSecret)
	resp, err := centerHTTP.Do(req)
	if err != nil {
		return true, "login center unreachable"
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return false, ""
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return true, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return false, fmt.Sprintf("HTTP %d, not retrying", resp.StatusCode)
}

// handleBackchannelLogout 收登录中心的退出通知（OpenID Connect Back-Channel Logout 1.0）：POST 表单 logout_token=…。
// 验过（中心签的 RS256、typ logout+jwt、iss 是中心、aud 是本系统、没过期、发证时间不在将来、有 jti、sid、events 里有退出事件、没有 nonce）
// 就删掉带这个 sid 的全部会话、后台撤销它们的 refresh_token，每个记一条「退出」（原因「在别的项目退出」）。
// 200 {ok}（一个都没有也算）；400 invalid_request（令牌不对，中心不再送）；429 rate_limited；503 unavailable（存储出错，中心会重送）。
func (s *SSO) handleBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	if !s.backchannelQuota.allow("") {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	raw := ""
	if r.ParseForm() == nil {
		raw = r.PostForm.Get("logout_token")
	}
	ctx, cancel := context.WithTimeout(r.Context(), upstreamLimit)
	defer cancel()
	c, err := s.centerToken(ctx, raw, "logout+jwt")
	if err == nil {
		_, hasEvent := c.Events[backchannelEvent]
		switch {
		case !hasEvent:
			err = errors.New("events lacks the back-channel logout event")
		case c.JTI == "":
			err = errors.New("no jti")
		case !sidFormat.MatchString(c.SID):
			err = errors.New("no sid")
		}
	}
	if err != nil {
		log.Printf("sso: backchannel logout refused: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	gone, err := s.store.DeleteSessionsBySID(ctx, c.SID)
	if err != nil {
		log.Printf("sso: backchannel logout: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "unavailable"})
		return
	}
	for i := range gone {
		s.locks.Delete(gone[i].IDHash)
		s.retryAfter.Delete(gone[i].IDHash)
		s.record(s.sessionEvent(loginEvent{}, &gone[i], eventLogout, "在别的项目退出"))
	}
	go func() {
		for _, sess := range gone {
			if rt, err := s.open("refresh-token", sess.RefreshSeal); err == nil {
				rctx, cancel := context.WithTimeout(context.Background(), upstreamLimit)
				s.revoke(rctx, string(rt))
				cancel()
			}
		}
	}()
	log.Printf("sso: backchannel logout: %d sessions ended", len(gone))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// centerClaims 是登录中心签的 sid_hint、退出令牌里本项目要看的几样。
type centerClaims struct {
	JTI    string                     `json:"jti"`
	SID    string                     `json:"sid"`
	State  string                     `json:"state"`
	Events map[string]json.RawMessage `json:"events"`
}

// centerToken 验登录中心签的 sid_hint 或退出令牌：头里 typ 对得上（不能拿 id_token 或另一种冒充）、centerVerifier 那几样、
// 有发证时间且不在将来、没有 nonce。
func (s *SSO) centerToken(ctx context.Context, raw, typ string) (centerClaims, error) {
	var c centerClaims
	head, _, _ := strings.Cut(raw, ".")
	hb, err := base64.RawURLEncoding.DecodeString(head)
	var h struct {
		Typ string `json:"typ"`
	}
	if err != nil || json.Unmarshal(hb, &h) != nil || !strings.EqualFold(h.Typ, typ) {
		return c, fmt.Errorf("not a %s", typ)
	}
	tok, err := s.centerVerifier(ctx).Verify(ctx, raw)
	if err != nil {
		return c, fmt.Errorf("%s: %w", typ, err)
	}
	if tok.IssuedAt.IsZero() || tok.IssuedAt.After(time.Now().Add(centerClockSkew)) {
		return c, fmt.Errorf("%s: iat missing or in the future", typ)
	}
	if tok.Nonce != "" {
		return c, fmt.Errorf("%s: nonce not allowed", typ)
	}
	if err := tok.Claims(&c); err != nil {
		return c, fmt.Errorf("%s: %w", typ, err)
	}
	return c, nil
}

// shortcut: 撤销失败只记日志不重试（令牌只在服务端，会话已删）；要做到必撤，就把加密的令牌连同删除一起写进待撤销表。
func (s *SSO) endSession(ctx context.Context, idHash string) (*Session, error) {
	sess, err := s.store.DeleteSession(ctx, idHash)
	if err != nil {
		return nil, err
	}
	s.locks.Delete(idHash)
	s.retryAfter.Delete(idHash)
	if sess != nil && !sess.Account {
		if rt, err := s.open("refresh-token", sess.RefreshSeal); err == nil {
			s.revoke(ctx, string(rt))
		}
	}
	return sess, nil
}

// revokeSession 下线并记一条「被收回」；会话已经不在（别的请求先踢了）就不记。
func (s *SSO) revokeSession(ctx context.Context, r *http.Request, idHash, reason string) {
	if gone, _ := s.endSession(ctx, idHash); gone != nil {
		s.recordRevoked(r, gone, reason)
	}
}

func (s *SSO) recordRevoked(r *http.Request, sess *Session, reason string) {
	ev := loginEvent{}
	if r != nil {
		ev = requestEvent(r, "")
	}
	s.record(s.sessionEvent(ev, sess, eventRevoked, reason))
}

func (s *SSO) consumeState(state string, expires int64) bool {
	now := time.Now().Unix()
	s.usedStates.Range(func(k, v any) bool {
		if v.(int64) < now {
			s.usedStates.Delete(k)
		}
		return true
	})
	_, used := s.usedStates.LoadOrStore(state, expires)
	return !used
}

// ---------- 停用名单 ----------

// syncWant 是攒着还没拉的名单请求：zero 是要一次在请求之后才发出的拉取（提醒带 0），max 是要的最低版本。
type syncWant struct {
	any, zero bool
	max       int64
}

// body 是问中台时带的：有 0 取 0，否则取最大的版本号；只是兜底（启动、对账）就带 {}。
func (w syncWant) body() map[string]any {
	switch {
	case w.zero:
		return map[string]any{"revision": 0}
	case w.max > 0:
		return map[string]any{"revision": w.max}
	}
	return map[string]any{}
}

// handleBlocklistChanged 收集成平台「名单变了」的提醒。提醒不带名单、不需要密钥：
// 假提醒最多让项目多问一次中台，名单只从中台读。版本 0 是要一份新拉的，比本地新的版本是要至少那么新的。
func (s *SSO) handleBlocklistChanged(w http.ResponseWriter, r *http.Request) {
	var hint struct {
		Revision json.RawMessage `json:"revision"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&hint)
	n, _ := jsNumber(hint.Revision)
	local, synced, err := s.store.BlocklistRevision(r.Context())
	switch {
	case n == 0:
		s.requestSync(syncWant{zero: true})
	case n > 0 && n <= maxSafeInteger && n == math.Trunc(n):
		if err != nil || !synced || int64(n) > local {
			s.requestSync(syncWant{max: int64(n)})
		}
	case err != nil || !synced:
		s.requestSync(syncWant{})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// requestSync 把一个名单请求并进待拉的（有 0 留 0，版本取大的），叫醒 syncLoop。
func (s *SSO) requestSync(w syncWant) {
	s.syncMu.Lock()
	s.syncWant = syncWant{any: true, zero: s.syncWant.zero || w.zero, max: max(s.syncWant.max, w.max)}
	s.syncMu.Unlock()
	select {
	case s.syncWake <- struct{}{}:
	default:
	}
}

// syncLoop 一轮拉一次名单，两轮之间至少隔 syncMinGap。一轮最多试 syncAttempts 次、退避重试；
// 都没成就把这轮的请求放回去，syncRetry 后再来一轮，直到成功。这期间用库里的旧名单（健康检查 blocklist_unreachable）。
func (s *SSO) syncLoop() {
	for range s.syncWake {
		s.syncMu.Lock()
		want := s.syncWant
		s.syncWant = syncWant{}
		s.syncMu.Unlock()
		if !want.any {
			continue
		}
		var err error
		for attempt := range syncAttempts {
			if attempt > 0 {
				time.Sleep(s.syncRetry / 60 << (attempt - 1))
			}
			if err = s.syncBlocklist(want); err == nil {
				break
			}
			log.Printf("sso: blocklist sync attempt %d: %v", attempt+1, err)
		}
		s.syncMu.Lock()
		s.syncFailing = err != nil
		s.syncMu.Unlock()
		if err != nil {
			time.AfterFunc(s.syncRetry, func() { s.requestSync(want) })
		}
		time.Sleep(s.syncMinGap)
	}
}

// syncBlocklist 问中台要一份停用名单（中台代读集成平台，几个项目、几个请求合成一次），校验、落库，再踢掉名单上的人。
func (s *SSO) syncBlocklist(want syncWant) error {
	hc := s.callHub(http.MethodPost, "/sso/blocklist", want.body(), blocklistTimeout)
	if hc.kind != hubOK {
		return errors.New(hc.why)
	}
	next, hubSyncedAt, err := blocklistFrom(hc.body)
	if err != nil {
		return err
	}
	s.syncMu.Lock()
	s.syncedAt = hubSyncedAt
	s.syncMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), upstreamLimit)
	defer cancel()
	// 先落名单再踢人：落盘失败就不换名单，下一轮再来；新登录也是按落盘的名单拒绝。
	if err := s.store.SaveBlocklist(ctx, next); errors.Is(err, ErrStaleBlocklist) {
		log.Printf("sso: blocklist revision %d is older than the stored one, ignored", next.Revision)
		return nil
	} else if err != nil {
		return fmt.Errorf("save blocklist: %w", err)
	}
	kicked, err := s.store.DeleteSessionsBySub(ctx, next.Entries)
	if err != nil {
		return fmt.Errorf("end blocked sessions: %w", err)
	}
	for _, sess := range kicked {
		s.locks.Delete(sess.IDHash)
		s.retryAfter.Delete(sess.IDHash)
		s.recordRevoked(nil, &sess, "停用名单："+next.Entries[sess.Identity.Sub].Reason)
	}
	go func() {
		for _, sess := range kicked {
			if rt, err := s.open("refresh-token", sess.RefreshSeal); err == nil {
				rctx, cancel := context.WithTimeout(context.Background(), upstreamLimit)
				s.revoke(rctx, string(rt))
				cancel()
			}
		}
	}()
	log.Printf("sso: blocklist revision %d, %d blocked, %d sessions ended", next.Revision, len(next.Entries), len(kicked))
	return nil
}

// blocklistFrom 严格读中台回的名单，和中台的校验同一规则：没读全不收，有一条不对整份不收。
// 第二个返回值是中台上次真正拉到集成平台名单的时间（synced_at）。
func blocklistFrom(b map[string]any) (Blocklist, time.Time, error) {
	malformed := errors.New("blocklist reply malformed")
	revision, ok := safeInt(b["revision"])
	syncedAt, ok2 := safeInt(b["synced_at"])
	complete, ok3 := b["complete"].(bool)
	list, ok4 := b["entries"].([]any)
	if !ok || !ok2 || syncedAt == 0 || !ok3 || !ok4 {
		return Blocklist{}, time.Time{}, malformed
	}
	if !complete {
		return Blocklist{}, time.Time{}, errors.New("blocklist not complete, keeping the old one")
	}
	next := Blocklist{Revision: revision, SyncedAt: time.Now(), Entries: map[string]BlockEntry{}}
	for _, v := range list {
		e, _ := v.(map[string]any)
		sub, ok := e["sub"].(string)
		reason, ok2 := e["reason"].(string)
		since, ok3 := e["since"].(string)
		if !ok || sub == "" || jsLen(sub) > 200 || !ok2 || jsLen(reason) > 500 || !ok3 || jsLen(since) > 100 {
			return Blocklist{}, time.Time{}, malformed
		}
		next.Entries[sub] = BlockEntry{Reason: reason, Since: since}
	}
	return next, time.UnixMilli(syncedAt), nil
}

// blocked 在登录时判断能不能放人。名单从没同步成功过，或者读不出来，一律不放：不按空名单放人。
func (s *SSO) blocked(ctx context.Context, sub string) (string, bool) {
	_, synced, err := s.store.BlocklistRevision(ctx)
	if err != nil {
		log.Printf("sso: load blocklist: %v", err)
		return "名单读不出来", true
	}
	if !synced {
		return "停用名单还没同步成功过", true
	}
	e, err := s.store.BlockedEntry(ctx, sub)
	if err != nil {
		log.Printf("sso: load blocklist: %v", err)
		return "名单读不出来", true
	}
	if e != nil {
		return e.Reason, true
	}
	return "", false
}

// ---------- 集成平台接口 ----------

type userInfo struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture string `json:"picture"`
}

func (s *SSO) userInfo(ctx context.Context, accessToken, wantSub string) (userInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.userInfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return userInfo{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return userInfo{}, fmt.Errorf("%w: userinfo HTTP 403", errDenied)
	case resp.StatusCode == http.StatusUnauthorized:
		return userInfo{}, fmt.Errorf("%w: userinfo HTTP 401", errTokenStale)
	case resp.StatusCode != http.StatusOK:
		return userInfo{}, fmt.Errorf("userinfo HTTP %d", resp.StatusCode)
	}
	var info userInfo
	if json.Unmarshal(body, &info) != nil {
		return userInfo{}, errors.New("userinfo reply malformed")
	}
	if info.Sub != wantSub {
		return userInfo{}, fmt.Errorf("%w: userinfo sub mismatch", errDenied)
	}
	return info, nil
}

// revoke 用手上的 SSO 密钥撤销令牌，失败只记 stderr。
func (s *SSO) revoke(ctx context.Context, refreshToken string) {
	if refreshToken == "" {
		return
	}
	s.hubMu.Lock()
	sec, ok := s.usableLocked()
	s.hubMu.Unlock()
	if !ok {
		log.Printf("sso: revoke skipped: no usable SSO client secret")
		return
	}
	form := url.Values{"token": {refreshToken}, "token_type_hint": {"refresh_token"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.revokeURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(s.oauth.ClientID), url.QueryEscape(sec.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("sso: revoke: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("sso: revoke HTTP %d", resp.StatusCode)
	}
}

// token 端点（换码、刷新）失败的四类。
const (
	// 400 且 JSON error=invalid_grant：这个人被拒，下线、撤销。
	tokenRefused = "refused"
	// 401 或 invalid_client：本应用的 SSO 密钥不对，找中台重取；不宽限，不下线。
	tokenSecret = "secret"
	// 429、5xx、断线、超时、回包不对：暂时不可用，给宽限。
	tokenUnavailable = "unavailable"
	// 其余 4xx（含纯文本 403）：说不清，会话回 503，不宽限；从第一次起过了一个核对周期还这样就当被拒、下线。
	tokenUnclear = "unclear"
)

// oauthCodes 是能写进 stderr 的 OAuth 错误码：集成平台回包里的别的东西一概不记。
var oauthCodes = map[string]bool{"invalid_request": true, "invalid_client": true, "invalid_grant": true, "unauthorized_client": true,
	"unsupported_grant_type": true, "invalid_scope": true, "access_denied": true, "server_error": true, "temporarily_unavailable": true}

// tokenTrouble 给 token 端点的失败归类。错误码只从 JSON 回包的 error 读；why 只带状态码和 oauthCodes 里的错误码。
func tokenTrouble(err error) (kind, why string) {
	if err == nil {
		return "", ""
	}
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || re.Response == nil {
		var ue *url.Error
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return tokenUnavailable, "timeout"
		case errors.As(err, &ue):
			return tokenUnavailable, "network error"
		}
		return tokenUnavailable, "reply malformed"
	}
	status := re.Response.StatusCode
	var body struct {
		Error any `json:"error"`
	}
	_ = json.Unmarshal(re.Body, &body)
	code, _ := body.Error.(string)
	if !oauthCodes[code] {
		code = ""
	}
	why = strings.TrimSpace(fmt.Sprintf("HTTP %d %s", status, code))
	switch {
	case status == http.StatusUnauthorized || code == "invalid_client":
		return tokenSecret, why
	case status == http.StatusBadRequest && code == "invalid_grant":
		return tokenRefused, why
	case status == http.StatusTooManyRequests || status >= 500 || status < 400:
		return tokenUnavailable, why
	}
	return tokenUnclear, why
}

// ---------- SSO 应用密钥（中台发） ----------

// ssoSecret 是中台发的 SSO 应用密钥，只放内存：不落盘、不进日志。version 是中台算的带盐指纹，健康检查报它。
type ssoSecret struct {
	secret, version string
	fetchedAt       time.Time // 最后一次从中台拿到它
}

// oauthWith 是带上这把密钥的 oauth2 配置。
func (s *SSO) oauthWith(sec ssoSecret) *oauth2.Config {
	c := s.oauth
	c.ClientSecret = sec.secret
	return &c
}

// usableLocked 是手上的密钥能不能用：有，中台钥匙没坏，距最后一次从中台拿到不到 secretMaxAge，没被集成平台拒。调用方持有 hubMu。
func (s *SSO) usableLocked() (ssoSecret, bool) {
	sec := s.secret
	if sec.secret == "" || s.credentialBadAt != 0 || time.Since(sec.fetchedAt) >= s.secretMaxAge || s.rejectedLocked(sec.version) {
		return ssoSecret{}, false
	}
	return sec, true
}

// rejectedLocked 是这个版本刚被集成平台拒过。调用方持有 hubMu。
func (s *SSO) rejectedLocked(version string) bool {
	return version == s.secretRejected && time.Since(s.secretRejectAt) < secretRejectFor
}

// secretFor 返回能用的密钥。手上有、但距上次问中台超过 secretRefresh，就在后台顺手再问；
// 手上没有能用的就问中台（secretAskGap 内最多问一次），最多等 secretWait。
func (s *SSO) secretFor(ctx context.Context) (ssoSecret, bool) {
	s.hubMu.Lock()
	sec, ok := s.usableLocked()
	stale := time.Since(s.secretAskedAt) > secretRefresh
	s.hubMu.Unlock()
	if ok {
		if stale {
			s.askSecret()
		}
		return sec, true
	}
	wait := time.NewTimer(secretWait)
	defer wait.Stop()
	select {
	case <-s.askSecret():
	case <-wait.C:
	case <-ctx.Done():
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	return s.usableLocked()
}

// secretRefused 在集成平台不认这把密钥（401 或 invalid_client）时调：马上找中台重取（secretAskGap 内最多一次）。
// 中台给了别的版本就返回它；没给就记下这个版本被拒，secretRejectFor 内不再用它。
func (s *SSO) secretRefused(ctx context.Context, used ssoSecret) (ssoSecret, bool) {
	select {
	case <-s.askSecret():
	case <-ctx.Done():
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	if s.secret.version != used.version {
		return s.usableLocked()
	}
	if !s.rejectedLocked(used.version) {
		log.Printf("sso: the IdP refuses SSO client secret version %s and the hub has no other", used.version)
	}
	s.secretRejected, s.secretRejectAt = used.version, time.Now()
	return ssoSecret{}, false
}

var closedCh = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// askSecret 找中台要一次密钥：同一时刻只问一次，大家共用结果；距上次问不到 secretAskGap 就不问。
// 返回的通道在这次问完时关掉（没问就是已经关了的）。
func (s *SSO) askSecret() <-chan struct{} {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	if s.secretAsking != nil {
		return s.secretAsking
	}
	if time.Since(s.secretAskedAt) < secretAskGap {
		return closedCh
	}
	done := make(chan struct{})
	s.secretAsking, s.secretAskedAt = done, time.Now()
	go func() {
		s.fetchSecret()
		s.hubMu.Lock()
		s.secretAsking = nil
		s.hubMu.Unlock()
		close(done)
	}()
	return done
}

// fetchSecret 问一次中台 sso/client，按回包换掉或作废手上的密钥：钥匙不对、没登记、client_id 和 SSO_CLIENT_ID 不一样都作废；
// 中台连不上、回包不对就接着用手上的。
func (s *SSO) fetchSecret() {
	hc := s.callHub(http.MethodGet, "/sso/client", nil, hubTimeout)
	kind, why := hc.kind, hc.why
	var got ssoSecret
	switch {
	case hc.kind == hubCredential:
		why = "the hub does not accept SSO_HUB_KEY"
	case hc.kind != hubOK:
		kind = hubUnavailable
	case hc.body["result"] == "not_configured":
		why = "the hub has no SSO client secret for this system"
	case hc.body["result"] != "ok":
		kind, why = hubUnavailable, "hub /sso/client: reply malformed"
	default:
		id, _ := hc.body["client_id"].(string)
		secret, _ := hc.body["client_secret"].(string)
		version, _ := hc.body["version"].(string)
		switch {
		case id != s.oauth.ClientID:
			why = "the hub's client_id differs from SSO_CLIENT_ID"
		case secret == "" || !secretVersionFormat.MatchString(version):
			kind, why = hubUnavailable, "hub /sso/client: reply malformed"
		default:
			got = ssoSecret{secret: secret, version: version, fetchedAt: time.Now()}
		}
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	s.secretAskKind = kind
	// 只有比上次改动它更晚发出的调用能改：迟到的回包救不回旧密钥。
	if hc.seq < s.secretSeq {
		return
	}
	if kind != hubUnavailable {
		if got.version != "" && got.version != s.secret.version {
			log.Printf("sso: SSO client secret version %s from the hub", got.version)
		}
		s.secret, s.secretSeq = got, hc.seq
	}
	if why != s.secretWhy {
		if why != "" {
			log.Printf("sso: SSO client secret: %s", why)
		}
		s.secretWhy = why
	}
}

// ---------- 账号密码（经 FBIF 中台） ----------

// 项目不碰账号表，也不经手密码：密码交给登录中心，中心拿只能核密码的窄钥匙问中台，登对了把身份放进 id_token 交回本项目（handleCenterCallback）。
// 比密码、连错锁定、写「状态」都在中台（One-Api 第二实例，规则见 hub/login-hub.ts）；之后每个核对周期本项目自己找中台核对账号。
// 中台只回身份（record_id、账号名、能用的应用）、凭据版本（密码改了就变）和核验时间。
// 三类结果分开处理：账号或本应用被收回（下线，不给宽限）、钥匙不对或被撤（不给宽限，会话留着）、中台或账号表暂时不可用（只有它给宽限）。

// hubCall 是调一次中台的结果：kind 是下面四种之一，body 只在 hubOK 时有，why 是写进 stderr 的原因。
type hubCall struct {
	seq  int64
	kind string
	body map[string]any
	why  string
}

const (
	hubOK = "ok"
	// 断线、超时、5xx、429、回包不对：只有这种给登着的人宽限。
	hubUnavailable = "unavailable"
	// 钥匙不对、被撤了，或地址配错（401、403、404）：不给宽限，飞书登录和账号密码都受影响（手上的 SSO 密钥也不能用）。
	hubCredential = "credential"
	// 本应用的账号登录被停了（403 system_revoked）：账号会话全部下线。
	hubRevoked = "revoked"
)

// callHub 调一次中台。ok 是 HTTP 200 且回包是 JSON 对象。
func (s *SSO) callHub(method, path string, body any, timeout time.Duration) hubCall {
	s.hubMu.Lock()
	s.hubSeq++
	seq := s.hubSeq
	s.hubMu.Unlock()
	status, obj, err := s.hubDo(method, path, body, timeout)
	if err != nil {
		return hubCall{seq: seq, kind: hubUnavailable, why: fmt.Sprintf("hub %s: %v", path, err)}
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	// 账号表的列被改了时中台回 503 也带着表结构问题：照样记下，健康检查报得出是哪一列。sso/* 的回包不带表结构问题，不动它。
	if list, ok := obj["problems"].([]any); ok && !strings.HasPrefix(path, "/sso/") && (status == http.StatusOK || (status == http.StatusServiceUnavailable && obj["error"] == "source_unavailable")) {
		problems := []string{}
		for _, p := range list {
			if p, ok := p.(string); ok && hubProblemFormat.MatchString(p) {
				problems = append(problems, p)
			}
		}
		slices.Sort(problems)
		s.hubProblems = problems
	}
	switch {
	case status == http.StatusOK && obj != nil:
		if seq > s.credentialBadAt {
			s.credentialBadAt = 0
		}
		return hubCall{seq: seq, kind: hubOK, body: obj}
	case status == http.StatusForbidden && obj["error"] == "system_revoked":
		// 能说出「本应用被停了」的钥匙是对的。
		if seq > s.credentialBadAt {
			s.credentialBadAt = 0
		}
		s.markRevoked(seq)
		return hubCall{seq: seq, kind: hubRevoked}
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound:
		if s.credentialBadAt == 0 {
			log.Printf("sso: hub %s: HTTP %d, the hub does not accept SSO_HUB_KEY for SSO_HUB_URL", path, status)
		}
		s.credentialBadAt = max(s.credentialBadAt, seq)
		return hubCall{seq: seq, kind: hubCredential, why: fmt.Sprintf("hub %s: HTTP %d", path, status)}
	}
	why := fmt.Sprintf("hub %s: HTTP %d", path, status)
	if obj == nil {
		why += ", body not JSON"
	}
	return hubCall{seq: seq, kind: hubUnavailable, why: why}
}

// hubClient 调中台不跟跳转：钥匙只送到 SSO_HUB_URL。
var hubClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// hubDo 发一次请求、读回包：连不上、超时返回 err；回包不是 JSON 对象时 obj 是 nil。
// 超时只看自己的，不跟着浏览器的请求取消：中台那头已经开始算了，回包照样要记。
func (s *SSO) hubDo(method, path string, body any, timeout time.Duration) (int, map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var payload io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.hub.url+path, payload)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.hub.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hubClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var obj map[string]any
	if raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)); err != nil || json.Unmarshal(raw, &obj) != nil {
		obj = nil
	}
	return resp.StatusCode, obj, nil
}

// accountCall 调账号接口，顺带记下中台连不连得上。「本应用被停」只由拉开关（pullState）清：新登录前、兜底对账、开关提醒都会拉。
func (s *SSO) accountCall(path string, body any) hubCall {
	hc := s.callHub(http.MethodPost, path, body, hubTimeout)
	s.hubMu.Lock()
	s.hubDown = hc.kind == hubUnavailable
	s.hubMu.Unlock()
	return hc
}

// hubFlags 是此刻两个撤权标记在不在。
func (s *SSO) hubFlags() (revoked, credentialBad bool) {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	return s.revokedAt != 0, s.credentialBadAt != 0
}

// markRevoked 记下本应用的账号登录被停了，账号会话全部下线（不给宽限）；之后每个账号请求也会看到它。调用方持有 hubMu。
func (s *SSO) markRevoked(seq int64) {
	first := s.revokedAt == 0
	s.revokedAt = max(s.revokedAt, seq)
	if first {
		go s.revokeAccountSessions(revokedReason)
	}
}

func (s *SSO) revokeAccountSessions(reason string) {
	ctx := context.Background()
	sessions, err := s.store.AccountSessions(ctx)
	if err != nil {
		// 下线不了也不会放人：每个账号请求都先看 revokedAt。
		log.Printf("sso: revoke account sessions: %v", err)
		return
	}
	for _, sess := range sessions {
		s.revokeSession(ctx, nil, sess.IDHash, reason)
	}
	if len(sessions) > 0 {
		log.Printf("sso: %s, %d account sessions ended", reason, len(sessions))
	}
}

// pullState 拉一次系统开关：停了就全部下线；开着（在发现被停之后发出的才算）就恢复。
func (s *SSO) pullState() {
	hc := s.callHub(http.MethodGet, "/state", nil, hubTimeout)
	if hc.kind != hubOK {
		return
	}
	active, ok := hc.body["active"].(bool)
	revision, ok2 := hc.body["revision"].(float64)
	if !ok || !ok2 {
		return
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	s.stateRevision = max(s.stateRevision, revision)
	if !active {
		s.markRevoked(hc.seq)
	} else if hc.seq > s.revokedAt {
		s.revokedAt = 0
	}
}

// syncState 同一时间只拉一个，两次之间至少隔 statePullGap；拉的时候又来了提醒，拉完再拉一次。
func (s *SSO) syncState() {
	s.stateMu.Lock()
	if s.statePulling {
		s.stateAgain = true
		s.stateMu.Unlock()
		return
	}
	s.statePulling = true
	s.stateMu.Unlock()
	for {
		s.stateMu.Lock()
		s.stateAgain = false
		wait := time.Until(s.statePulledAt.Add(statePullGap))
		s.stateMu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
		s.stateMu.Lock()
		s.statePulledAt = time.Now()
		s.stateMu.Unlock()
		s.pullState()
		s.stateMu.Lock()
		if !s.stateAgain {
			s.statePulling = false
			s.stateMu.Unlock()
			return
		}
		s.stateMu.Unlock()
	}
}

// seenRevision 是见过的最新系统开关版本。
func (s *SSO) seenRevision() float64 {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	return s.stateRevision
}

// hubAccount 是中台说这个账号能用时回的身份和凭据版本。
type hubAccount struct {
	recordID, name, credential string
	apps                       []string
	verifiedAt                 float64 // 中台读到这一行的时间，毫秒
}

// parseHubAccount 读中台说这个账号能用时的回包；回包不对返回 false。
func parseHubAccount(b map[string]any) (hubAccount, bool) {
	a, _ := b["account"].(map[string]any)
	id, _ := a["record_id"].(string)
	name, nameOK := a["name"].(string)
	list, appsOK := a["apps"].([]any)
	if b["result"] != "ok" || !recordIDFormat.MatchString(id) || !nameOK || !appsOK {
		return hubAccount{}, false
	}
	apps := make([]string, 0, len(list))
	for _, v := range list {
		app, _ := v.(string)
		if !recordIDFormat.MatchString(app) {
			return hubAccount{}, false
		}
		apps = append(apps, app)
	}
	credential, _ := b["credential"].(string)
	verifiedAt, timeOK := b["verified_at"].(float64)
	if !credentialFormat.MatchString(credential) || !timeOK {
		return hubAccount{}, false
	}
	return hubAccount{recordID: id, name: clip(name, 100), apps: apps, credential: credential, verifiedAt: verifiedAt}, true
}

// freshAt 是中台的核验时间和本机差得不超过 hubFresh：差多了是时钟歪了，或回包被缓存、重放。
func freshAt(verifiedAt float64) bool {
	return math.Abs(float64(time.Now().UnixMilli())-verifiedAt) <= float64(hubFresh.Milliseconds())
}

func (s *SSO) markChecked(idHash string) {
	s.checkedMu.Lock()
	defer s.checkedMu.Unlock()
	// 满了就清空：多核对一次，不会多放人。
	if len(s.checkedHere) >= 100_000 {
		clear(s.checkedHere)
	}
	s.checkedHere[idHash] = true
}

func (s *SSO) wasCheckedHere(idHash string) bool {
	s.checkedMu.Lock()
	defer s.checkedMu.Unlock()
	return s.checkedHere[idHash]
}

// accountFresh 是账号会话这次不用找中台核对：本进程核对过、在核对周期内，本应用没被停，钥匙也没出问题。
func (s *SSO) accountFresh(sess *Session) bool {
	revoked, credentialBad := s.hubFlags()
	return s.wasCheckedHere(sess.IDHash) && time.Since(sess.CheckedAt) < s.checkEvery && !revoked && !credentialBad
}

// legacyAccountSession 是 v5 登录的账号会话：存的是 16 位十六进制的密码指纹，和中台的凭据版本对不上，第一次核对时请他重新登录一次。
func legacyAccountSession(sess *Session) bool {
	return !credentialFormat.MatchString(sess.AccountCredential)
}

// settleAccountSession 按中台对一个账号的核对结果处理一个账号会话：被收回（账号删了、停了、取消了本应用、本应用被停）
// 或凭据版本变了（密码改了）就下线，返回 errRejected；中台暂时不可用、不认钥匙、回包不对返回 errUnavailable（会话不动）；
// 否则更新名字和能用的应用、记下核验时间，返回更新后的会话。
func (s *SSO) settleAccountSession(ctx context.Context, r *http.Request, sess Session, hc hubCall) (*Session, error) {
	if legacyAccountSession(&sess) {
		s.revokeSession(ctx, r, sess.IDHash, legacyReason)
		return nil, errRejected
	}
	revoked, credentialBad := s.hubFlags()
	if hc.kind == hubRevoked || (hc.kind == hubOK && revoked) {
		s.revokeSession(ctx, r, sess.IDHash, revokedReason)
		return nil, errRejected
	}
	if hc.kind != hubOK {
		log.Printf("sso: check account %s: %s", sess.Identity.Sub, hc.why)
		return nil, errUnavailable
	}
	if credentialBad {
		return nil, errUnavailable
	}
	if hc.body["result"] == "revoked" {
		reason, _ := hc.body["reason"].(string)
		reason = cmpOr(clip(reason, 100), "账号已停用")
		log.Printf("sso: account %s %s, ending session", sess.Identity.Sub, reason)
		s.revokeSession(ctx, r, sess.IDHash, reason)
		return nil, errRejected
	}
	acct, ok := parseHubAccount(hc.body)
	if !ok || acct.recordID != sess.Identity.Sub || !freshAt(acct.verifiedAt) {
		what := "malformed"
		if ok {
			what = "stale or for another record"
		}
		log.Printf("sso: check account %s: hub reply %s", sess.Identity.Sub, what)
		return nil, errUnavailable
	}
	if acct.credential != sess.AccountCredential {
		log.Printf("sso: account %s 密码已改, ending session", sess.Identity.Sub)
		s.revokeSession(ctx, r, sess.IDHash, "密码已改")
		return nil, errRejected
	}
	next := sess
	next.CheckedAt = time.Now()
	next.Identity.Name, next.Identity.Apps = acct.name, acct.apps
	if err := s.store.UpdateSession(ctx, next, sess.Version); err != nil {
		// 版本对不上（别处刚核对过或踢了）：以库里为准，本进程这次不算核对过。
		again, gerr := s.store.GetSession(ctx, sess.IDHash)
		if gerr != nil {
			return nil, errUnavailable
		}
		if again == nil {
			return nil, errRejected
		}
		return again, nil
	}
	s.markChecked(sess.IDHash)
	next.Version = sess.Version + 1
	return &next, nil
}

// checkAccount 每个核对周期找中台核对一次：被收回、改了密码就下线，否则更新名字和能用的应用。
// 中台或账号表暂时不可用时普通接口最多再放行 15 分钟；被停、钥匙不对不放行。
func (s *SSO) checkAccount(ctx context.Context, r *http.Request, sess *Session) (*Session, int) {
	mu, _ := s.locks.LoadOrStore(sess.IDHash, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	cur, err := s.store.GetSession(ctx, sess.IDHash)
	if err != nil {
		return nil, http.StatusServiceUnavailable
	}
	if cur == nil {
		return nil, http.StatusUnauthorized
	}
	if revoked, _ := s.hubFlags(); revoked {
		s.revokeSession(ctx, r, cur.IDHash, revokedReason)
		return nil, http.StatusUnauthorized
	}
	if legacyAccountSession(cur) {
		s.revokeSession(ctx, r, cur.IDHash, legacyReason)
		return nil, http.StatusUnauthorized
	}
	if s.accountFresh(cur) {
		return cur, http.StatusOK
	}
	// 宽限只给本进程核对成功过的会话，而且只在「中台暂时不可用」时给（钥匙被撤不给）。核对完再判：这次可能刚发现钥匙被撤。
	inGrace := func() bool {
		_, credentialBad := s.hubFlags()
		return s.wasCheckedHere(cur.IDHash) && !credentialBad && time.Since(cur.CheckedAt) < s.checkEvery+outageGrace
	}
	if n, ok := s.retryAfter.Load(cur.IDHash); ok && time.Now().Before(n.(retryNote).until) {
		if inGrace() {
			return cur, http.StatusOK
		}
		return nil, http.StatusServiceUnavailable
	}
	checked, err := s.settleAccountSession(ctx, r, *cur, s.accountCall("/account/check", map[string]any{"record_id": cur.Identity.Sub}))
	if errors.Is(err, errUnavailable) {
		s.retryAfter.Store(cur.IDHash, retryNote{until: time.Now().Add(outageBackoff), grace: true})
		if inGrace() {
			return cur, http.StatusOK
		}
		return nil, http.StatusServiceUnavailable
	}
	s.retryAfter.Delete(cur.IDHash)
	if err != nil {
		return nil, http.StatusUnauthorized
	}
	return checked, http.StatusOK
}

// ---------- 兜底对账、账号变更提醒 ----------

// reconcileLoop 是事件丢了时的兜底，跟着进程启动、不会停：先跑一轮，之后每 reconcileEvery 一轮。
// 一轮：重拉停用名单；待写队列有积压就马上排一次；再问一次 SSO 密钥、拉系统开关、把账号会话找中台核对一遍（reconcileOnce），
// 中台暂时不可用按 1 秒起翻倍重试（最长 5 分钟），成功了再等下一轮。
func (s *SSO) reconcileLoop() {
	for {
		s.requestSync(syncWant{})
		s.outbox.kickBacklog()
		backoff := time.Second
		for !s.reconcileOnce() {
			time.Sleep(backoff)
			backoff = min(backoff*2, outboxMaxBackoff, s.reconcileEvery)
		}
		time.Sleep(s.reconcileEvery)
	}
}

// reconcileOnce 是兜底对账一轮里找中台的部分：问一次 SSO 密钥（轮换了、被撤了不等有人登录就知道），拉一次系统开关，
// 再把每个账号会话核对一遍。中台暂时不可用或不认钥匙返回 false（退避后再来）。
func (s *SSO) reconcileOnce() bool {
	<-s.askSecret()
	s.hubMu.Lock()
	asked := s.secretAskKind
	s.hubMu.Unlock()
	if asked == hubUnavailable || asked == hubCredential {
		return false
	}
	s.syncState()
	ctx := context.Background()
	sessions, err := s.store.AccountSessions(ctx)
	if err != nil {
		log.Printf("sso: reconcile: load account sessions: %v", err)
		return false
	}
	bySub := map[string][]Session{}
	for _, sess := range sessions {
		bySub[sess.Identity.Sub] = append(bySub[sess.Identity.Sub], sess)
	}
	ended := 0
	for sub, list := range bySub {
		sentAt := time.Now()
		hc := s.accountCall("/account/check", map[string]any{"record_id": sub})
		if hc.kind == hubUnavailable || hc.kind == hubCredential {
			log.Printf("sso: reconcile: %s", hc.why)
			return false
		}
		for _, sess := range list {
			// 这次核对发出之后才登录或核对过的会话不动：它比这次的结果新。
			if sess.CheckedAt.After(sentAt) {
				continue
			}
			if _, err := s.settleAccountSession(ctx, nil, sess, hc); errors.Is(err, errRejected) {
				ended++
			}
		}
	}
	log.Printf("sso: reconcile: %d accounts checked, %d sessions ended", len(bySub), ended)
	return true
}

// handleAccountsChanged 收两种账号变更提醒。都不需要密钥，伪造了也无害：马上回 202、不回任何数据，只当场问中台。
// {"record_id":"rec…"}：共用账号表的两条自动化在人改了账号后发给「系统」表里填了通知地址的每个应用（所以会收到所有账号的变化）。
// {"revision":N}：中台改了本应用的账号登录开关；N 比见过的新或为 0 才去拉开关。
func (s *SSO) handleAccountsChanged(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RecordID string          `json:"record_id"`
		Revision json.RawMessage `json:"revision"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	w.WriteHeader(http.StatusAccepted)
	if in.Revision != nil {
		if n, ok := jsNumber(in.Revision); ok && (n == 0 || n > s.seenRevision()) {
			go s.syncState()
		}
		return
	}
	if recordIDFormat.MatchString(in.RecordID) && s.claimChange(in.RecordID) {
		go s.processChanges(in.RecordID)
	}
}

// claimChange 同一行正在处理时只记一笔「处理完再来一遍」；全局最近一分钟最多处理 60 次，多的丢掉（用户下一个请求照样会核对）。
func (s *SSO) claimChange(id string) bool {
	s.changedMu.Lock()
	defer s.changedMu.Unlock()
	if _, busy := s.changing[id]; busy {
		s.changing[id] = true
		return false
	}
	if !s.changeQuota.allow("") {
		return false
	}
	s.changing[id] = false
	return true
}

// processChanges 处理一行；处理期间又来了提醒就再来一遍，再来的这一遍也占每分钟 60 次的名额，没名额就不来了。
func (s *SSO) processChanges(id string) {
	for {
		s.applyAccountChange(id)
		s.changedMu.Lock()
		if !s.changing[id] || !s.changeQuota.allow("") {
			delete(s.changing, id)
			s.changedMu.Unlock()
			return
		}
		s.changing[id] = false
		s.changedMu.Unlock()
	}
}

// applyAccountChange 请中台核对那一行（顺带让中台把它和同名几行的「状态」对一遍），本应用里绑在这一行上的会话按结果下线或更新。
// 本应用里没有它的会话也问：中台要顺带对状态。
func (s *SSO) applyAccountChange(id string) {
	ctx := context.Background()
	sentAt := time.Now()
	hc := s.accountCall("/account/check", map[string]any{"record_id": id, "changed": true})
	sessions, err := s.store.AccountSessions(ctx)
	if err != nil {
		log.Printf("sso: accounts-changed %s: load sessions: %v", id, err)
	}
	for _, sess := range sessions {
		// 这次核对发出之后才登录或核对过的会话不动：它比这次的结果新。
		if sess.Identity.Sub == id && !sess.CheckedAt.After(sentAt) {
			s.settleAccountSession(ctx, nil, sess, hc)
		}
	}
}

// ---------- 健康检查 ----------

// handleHealth 给监控读：不带个人信息、表 ID、错误原文。有问题回 503。
// sso_secret_version 是手上 SSO 密钥的版本（中台算的带盐指纹），重置密钥后看它换没换；
// blocklist_revision 是库里名单的版本，blocklist_synced_at 是中台上次真正拉到集成平台名单的时间。
func (s *SSO) handleHealth(w http.ResponseWriter, r *http.Request) {
	revision, synced, storeErr := s.store.BlocklistRevision(r.Context())
	s.hubMu.Lock()
	problems := append([]string{}, s.hubProblems...)
	if s.hubDown {
		problems = append(problems, "accounts_unreachable")
	}
	if s.credentialBadAt != 0 {
		problems = append(problems, "hub_credential_invalid")
	}
	if s.centerRefused {
		problems = append(problems, "center_credential_invalid")
	}
	// 本应用的账号登录是管理员在中台停的，不是故障：只提醒。
	if s.revokedAt != 0 {
		problems = append(problems, "warn_account_login_stopped")
	}
	if s.logNearFull {
		problems = append(problems, "warn_log_table_near_full")
	}
	var secretVersion any
	if s.secret.secret != "" {
		secretVersion = s.secret.version
	}
	if _, ok := s.usableLocked(); !ok {
		// 被拒是单指：手上这把别的都好，只是集成平台不认、中台也没有更新的。
		if s.secret.secret != "" && s.rejectedLocked(s.secret.version) && s.credentialBadAt == 0 && time.Since(s.secret.fetchedAt) < s.secretMaxAge {
			problems = append(problems, "sso_secret_rejected")
		} else {
			problems = append(problems, "sso_secret_unavailable")
		}
	}
	s.hubMu.Unlock()
	var blocklistRevision, blocklistSyncedAt any
	if storeErr != nil || !synced {
		problems = append(problems, "blocklist_unsynced")
	} else {
		blocklistRevision = revision
	}
	s.syncMu.Lock()
	failing, syncedAt := s.syncFailing, s.syncedAt
	s.syncMu.Unlock()
	if failing {
		problems = append(problems, "blocklist_unreachable")
	}
	if !syncedAt.IsZero() {
		blocklistSyncedAt = syncedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		if time.Since(syncedAt) > blocklistStaleAfter {
			problems = append(problems, "blocklist_stale")
		}
	}
	oldest := 0
	st := s.outbox.stats()
	if !st.oldest.IsZero() {
		oldest = int(time.Since(st.oldest).Seconds())
	}
	for class, warn := range map[string]string{rowRequest: "warn_request_rows_dropped", rowLoginFail: "warn_login_rows_dropped"} {
		if at := st.droppedAt[class]; !at.IsZero() && time.Since(at) < droppedWarnFor {
			problems = append(problems, warn)
		}
	}
	if time.Duration(oldest)*time.Second > outboxStaleAfter {
		problems = append(problems, "outbox_stale")
	}
	if st.pending > outboxBacklogLimit {
		problems = append(problems, "outbox_backlog")
	}
	if st.saveFailed {
		problems = append(problems, "outbox_write_failed")
	}
	slices.Sort(problems)
	ok := !slices.ContainsFunc(problems, func(p string) bool { return !strings.HasPrefix(p, "warn_") })
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"ok": ok, "outbox_pending": st.pending, "oldest_pending_seconds": oldest,
		"dropped_request_rows": st.dropped[rowRequest], "dropped_login_rows": st.dropped[rowLoginFail], "problems": problems,
		"sso_secret_version": secretVersion, "blocklist_revision": blocklistRevision, "blocklist_synced_at": blocklistSyncedAt})
}

// ---------- 登录日志 ----------

// loginEvent 是日志表里的一行。密码、令牌、授权码、state、cookie、请求体、查询串一律不进来。
type loginEvent struct {
	Event, Reason, Method                  string
	Company, Subject, Name, Email, Account string
	RecordID                               string
	IP, IPSource, Browser, Next, Session   string
	// 只有「请求」行有：HTTP 方法、不带查询串的路径、状态码、耗时
	Verb, Path string
	Status     int
	DurationMS int64
	mergeKey   string // 一样的行合成一行：请求按方法、路径、状态码、人（没登录按 IP），登录失败按方式、IP、账号、原因
}

func requestEvent(r *http.Request, method string) loginEvent {
	ip, source := clientIP(r)
	return loginEvent{Method: method, IP: ip, IPSource: source, Browser: r.UserAgent()}
}

func withIdentity(ev loginEvent, id Identity) loginEvent {
	ev.Company, ev.Subject, ev.Name, ev.Email = id.CompanyCode, id.Sub, id.Name, id.Email
	return ev
}

func (s *SSO) sessionEvent(ev loginEvent, sess *Session, event, reason string) loginEvent {
	ev = withIdentity(ev, sess.Identity)
	ev.Event, ev.Reason, ev.Session = event, reason, sessionTag(sess.IDHash)
	ev.Method = methodFeishu
	if sess.Account {
		ev.Method, ev.RecordID, ev.Account = methodAccount, sess.Identity.Sub, sess.Identity.Name
	}
	return ev
}

func (e loginEvent) summary() string {
	var who string
	switch {
	case e.Method == methodAccount && (e.Account != "" || e.Name != ""):
		who = "账号 " + cmpOr(e.Account, e.Name) + " "
	case e.Method == methodAccount:
		who = "账号密码"
	case e.Name != "":
		who = e.Name + "（飞书）"
	case e.Event == eventRequest && e.Company == "":
		who = "未登录 "
	default:
		who = "飞书"
	}
	what := e.Event
	if e.Event == eventRequest {
		if !strings.HasSuffix(who, " ") && !strings.HasSuffix(who, "）") {
			who += " "
		}
		what = fmt.Sprintf("%s %s %d", e.Verb, e.Path, e.Status)
	}
	if e.Reason == "" {
		return who + what
	}
	return who + what + "：" + e.Reason
}

// typeAndResult 把事件拆成日志的「类型」「结果」；被收回不写结果。
func (e loginEvent) typeAndResult() (string, string) {
	switch e.Event {
	case eventLoginOK:
		return typeLogin, resultOK
	case eventLoginFail:
		return typeLogin, resultFail
	case eventLogout:
		return typeLogout, resultOK
	case eventRequest:
		if e.Status < 400 {
			return typeRequest, resultOK
		}
		return typeRequest, resultFail
	}
	return typeRevoked, ""
}

// record 把一条日志排进待写队列（「项目」由中台按钥匙填）。登录和请求从不等它。
func (s *SSO) record(e loginEvent) {
	typ, result := e.typeAndResult()
	if e.Event == eventLoginFail {
		// 不用登录就能造出来的行：一样的合成一行记次数，再按额度限量，不能拿它刷满共用的日志表。
		e.mergeKey = strings.Join([]string{rowLoginFail, e.Method, e.IP, e.Account, e.Reason}, "\x00")
	}
	f := map[string]any{"摘要": clip(e.summary(), 200), "时间": time.Now().UnixMilli(), "类型": typ, "次数": 1}
	if result != "" {
		f["结果"] = result
	}
	if e.Status != 0 {
		f["状态码"], f["耗时毫秒"] = e.Status, e.DurationMS
	}
	for _, kv := range []struct {
		col, val string
		max      int
	}{
		{"原因", e.Reason, 200}, {"登录方式", e.Method, 20}, {"公司", e.Company, 50},
		{"用户标识", e.Subject, 100}, {"姓名", e.Name, 100}, {"邮箱", e.Email, 200}, {"账号", e.Account, 100},
		{"记录ID", e.RecordID, 100}, {"方法", e.Verb, 10}, {"路径", e.Path, 500},
		{"IP", e.IP, 100}, {"IP来源", e.IPSource, 50}, {"浏览器", e.Browser, 500}, {"登录后去哪", e.Next, 500}, {"会话", e.Session, 12},
	} {
		if kv.val != "" {
			f[kv.col] = clip(kv.val, kv.max)
		}
	}
	s.outbox.add(outboxItem{Fields: f, Merge: e.mergeKey})
}

// ---------- 请求日志 ----------

// 本模板自己的路由不记请求日志：登录、退出另有登录日志，/api/me 和两个提醒、健康检查是探针。
var ownPaths = map[string]bool{
	loginPath: true, callbackPath: true, centerLoginPath: true, centerCallbackPath: true, centerFeishuPath: true, logoutPath: true, mePath: true,
	blocklistPath: true, accountsChangedPath: true, healthPath: true, backchannelPath: true,
}

type noteKey struct{}

// requestNote 是 Middleware 给每个请求开的小本子，RequireUser、Audit 往里记。
type requestNote struct {
	mu           sync.Mutex
	sess         *Session
	fresh, audit bool
}

func noteOf(r *http.Request) *requestNote {
	n, _ := r.Context().Value(noteKey{}).(*requestNote)
	return n
}

// Audit 让这个请求一定记进日志：导出、下载这类要留痕的接口在处理函数里调一下。要在 Middleware 包住的请求里才起作用。
func Audit(r *http.Request) {
	if n := noteOf(r); n != nil {
		n.mu.Lock()
		n.audit = true
		n.mu.Unlock()
	}
}

// Middleware 包住整个 mux 记请求日志，只记这几种：401、403、5xx、改数据的请求（POST/PUT/PATCH/DELETE，任何状态）、
// 敏感接口（RequireFreshUser）、调了 Audit 的接口。普通的成功 GET 和 404 不记。只记方法、不带查询串的路径、状态码、耗时、
// 谁（有会话时）、IP、浏览器；请求体、查询串、cookie 和别的请求头一概不记。
func (s *SSO) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ownPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		note := &requestNote{}
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				s.logRequest(r, note, http.StatusInternalServerError, time.Since(start))
				panic(p)
			}
			s.logRequest(r, note, cmpOrInt(sw.status, http.StatusOK), time.Since(start))
		}()
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), noteKey{}, note)))
	})
}

func (s *SSO) logRequest(r *http.Request, note *requestNote, status int, took time.Duration) {
	note.mu.Lock()
	sess, fresh, audit := note.sess, note.fresh, note.audit
	note.mu.Unlock()
	mutating := r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete
	if !(status == http.StatusUnauthorized || status == http.StatusForbidden || status >= 500 || mutating || fresh || audit) {
		return
	}
	ev := requestEvent(r, "")
	ev.Event, ev.Verb, ev.Path, ev.Status, ev.DurationMS = eventRequest, r.Method, clip(r.URL.Path, 500), status, took.Milliseconds()
	switch {
	case status == http.StatusUnauthorized:
		ev.Reason = "没登录"
	case status == http.StatusForbidden:
		ev.Reason = "没有权限"
	case status >= 500:
		ev.Reason = "服务出错"
	}
	sid := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		sid = c.Value
	}
	// 查会话、排队都放到后台，响应不等它。
	go func() {
		if sess == nil && sid != "" {
			ctx, cancel := context.WithTimeout(context.Background(), upstreamLimit)
			sess, _ = s.store.GetSession(ctx, hashID(sid))
			cancel()
		}
		who := "ip:" + ev.IP
		if sess != nil {
			ev = s.sessionEvent(ev, sess, eventRequest, ev.Reason)
			who = sess.Identity.Key()
		}
		ev.mergeKey = strings.Join([]string{rowRequest, ev.Verb, ev.Path, strconv.Itoa(status), who}, "\x00")
		s.record(ev)
	}()
}

// statusWriter 记下状态码；Flush、Hijack 原样转给底下的 ResponseWriter（长连接、WebSocket 照常用）。
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func cmpOrInt(v, fallback int) int {
	if v != 0 {
		return v
	}
	return fallback
}

// clientIP 取访客 IP，并记下用的是哪个。直连的是本机或内网（就是前面的反向代理）时，认 X-Forwarded-For 最右边那个：
// 那是反向代理自己加的（Caddy 默认不信访客传来的 X-Forwarded-For、换成真实地址；nginx 的 $proxy_add_x_forwarded_for
// 把真实地址追加在最后），左边的都可能是访客自己写的。X-Real-IP 不认：Caddy 原样转发访客传来的值，谁都能伪造。
// 直连的是公网地址（前面没有反向代理），这些头一概不认，用直连地址。前面还有一层 CDN 时记到的是 CDN 的地址。
func clientIP(r *http.Request) (string, string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if peer, err := netip.ParseAddr(host); err == nil && (peer.Unmap().IsLoopback() || peer.Unmap().IsPrivate()) {
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
			return clip(last, 100), "X-Forwarded-For"
		}
	}
	return host, "RemoteAddr"
}

// sessionTag 是会话 ID 哈希的前 12 位，日志里能把同一个会话的几行串起来，又倒推不出会话 ID。
func sessionTag(idHash string) string {
	if len(idHash) > 12 {
		return idHash[:12]
	}
	return idHash
}

var codeChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// cleanCode 只留外部传来的错误码里的安全字符。
func cleanCode(v string) string { return clip(codeChars.ReplaceAllString(v, ""), 64) }

func clip(v string, max int) string {
	if r := []rune(v); len(r) > max {
		return string(r[:max])
	}
	return v
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------- 小工具 ----------

func (s *SSO) seal(purpose string, plain []byte) string {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, []byte(purpose)))
}

func (s *SSO) open(purpose, sealed string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return nil, errors.New("sso: bad seal")
	}
	n := s.aead.NonceSize()
	return s.aead.Open(nil, raw[:n], raw[n:], []byte(purpose))
}

// perMinute 数每个 key 最近一分钟放行了几次，到了上限就不放（key 用 "" 就是全局一个数）。
type perMinute struct {
	limit  int
	mu     sync.Mutex
	hits   map[string][]time.Time
	pruned time.Time
}

func newPerMinute(limit int) *perMinute {
	return &perMinute{limit: limit, hits: map[string][]time.Time{}}
}

func (p *perMinute) allow(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if now.Sub(p.pruned) > time.Minute {
		for k, ts := range p.hits {
			if now.Sub(ts[len(ts)-1]) > time.Minute {
				delete(p.hits, k)
			}
		}
		p.pruned = now
	}
	ts := slices.DeleteFunc(p.hits[key], func(t time.Time) bool { return now.Sub(t) > time.Minute })
	if len(ts) >= p.limit {
		p.hits[key] = ts
		return false
	}
	p.hits[key] = append(ts, now)
	return true
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("sso: bad %s %q", key, v)
	}
	return d, nil
}

// reconcileInterval 把 SSO_RECONCILE_SECONDS 换成兜底对账间隔，note 接在启动日志的变量名后面。值是人在表里改的，
// 写错不能让服务起不来：看不懂用默认，超出 10 分钟～24 小时按边界算。每轮拉一次名单（集成平台按运行次数计费），
// 所以短于 10 分钟只给合规测试（allowShort：设了 SSO_CHECK_INTERVAL）用。
func reconcileInterval(raw string, allowShort bool) (time.Duration, string) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return defaultReconcile, " not set, default"
	}
	n, err := strconv.ParseUint(v, 10, 64)
	switch {
	case err != nil || n == 0:
		return defaultReconcile, " is not a whole number of seconds, default"
	case n < uint64(minReconcile/time.Second) && !allowShort:
		return minReconcile, " is under 10m, raised to it"
	case n > uint64(maxReconcile/time.Second):
		return maxReconcile, " is over 24h, lowered to it"
	}
	return time.Duration(n) * time.Second, ""
}

var recordIDFormat = regexp.MustCompile(`^rec[A-Za-z0-9]{4,40}$`)

// jsLen 是字符串按 UTF-16 算的长度：中台是 Node，按这个数限长。
func jsLen(v string) int {
	n := 0
	for _, r := range v {
		n += utf16.RuneLen(r)
	}
	return n
}

// jsNumber 照 Node 的 Number() 读提醒里的版本号：数字原样，null、false、空串算 0，true 算 1，数字样的字符串照读；别的不认。
func jsNumber(raw json.RawMessage) (float64, bool) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return 0, false
	}
	switch x := v.(type) {
	case nil:
		return 0, true
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if x = strings.TrimSpace(x); x == "" {
			return 0, true
		}
		n, err := strconv.ParseFloat(x, 64)
		return n, err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
	}
	return 0, false
}

// maxSafeInteger 是 JS 能精确表示的最大整数：中台是 Node，版本号、时间戳超过它就不准了。
const maxSafeInteger = 1<<53 - 1

// safeInt 读 JSON 里的非负安全整数。
func safeInt(v any) (int64, bool) {
	n, ok := v.(float64)
	if !ok || n < 0 || n > maxSafeInteger || n != math.Trunc(n) {
		return 0, false
	}
	return int64(n), true
}

// origin 是网址的 scheme://host:port（默认端口补上、主机名小写），解析不了或带账号密码返回 ""。
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return ""
	}
	port := u.Port()
	switch {
	case port != "":
	case u.Scheme == "https":
		port = "443"
	case u.Scheme == "http":
		port = "80"
	default:
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashID(sid string) string {
	sum := sha256.Sum256([]byte(sid))
	return hex.EncodeToString(sum[:])
}

func txnCookieName(state string) string {
	if len(state) > 16 {
		state = state[:16]
	}
	return txnCookiePrefix + state
}

// safeNext 只接受本站路径，登录相关页面不算，和前端 safeNext 同一规则。
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.ContainsFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ""
	}
	for _, p := range []string{"/login", "/auth"} {
		if next == p || strings.HasPrefix(next, p+"/") || strings.HasPrefix(next, p+"?") || strings.HasPrefix(next, p+"#") {
			return ""
		}
	}
	return next
}

func setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ---------- 单实例文件存储 ----------

// FileStore 把会话和名单存在一个 JSON 文件里；每次改动先写盘，写盘失败就撤回内存里的改动。
// shortcut: 只适合单实例部署，多实例或已有数据库时用项目的库实现 Store。
type FileStore struct {
	path string
	mu   sync.Mutex
	data struct {
		Sessions  map[string]Session `json:"sessions"`
		Blocklist Blocklist          `json:"blocklist"`
	}
}

func NewFileStore(path string) (*FileStore, error) {
	fs := &FileStore{path: path}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &fs.data); err != nil {
			return nil, fmt.Errorf("sso: %s: %w", path, err)
		}
	}
	if fs.data.Sessions == nil {
		fs.data.Sessions = map[string]Session{}
	}
	if fs.data.Blocklist.Entries == nil {
		fs.data.Blocklist.Entries = map[string]BlockEntry{}
	}
	return fs, nil
}

func (fs *FileStore) commit(undo func()) error {
	raw, err := json.MarshalIndent(fs.data, "", "  ")
	if err == nil {
		tmp := fs.path + ".tmp"
		if err = os.WriteFile(tmp, raw, 0o600); err == nil {
			err = os.Rename(tmp, fs.path)
		}
	}
	if err != nil {
		undo()
	}
	return err
}

func (fs *FileStore) CreateSession(_ context.Context, s Session) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.data.Sessions[s.IDHash] = s
	return fs.commit(func() { delete(fs.data.Sessions, s.IDHash) })
}

func (fs *FileStore) GetSession(_ context.Context, idHash string) (*Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	s, ok := fs.data.Sessions[idHash]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (fs *FileStore) UpdateSession(_ context.Context, s Session, expectVersion int64) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	cur, ok := fs.data.Sessions[s.IDHash]
	if !ok || cur.Version != expectVersion {
		return ErrConflict
	}
	s.Version = expectVersion + 1
	fs.data.Sessions[s.IDHash] = s
	return fs.commit(func() { fs.data.Sessions[s.IDHash] = cur })
}

func (fs *FileStore) DeleteSession(_ context.Context, idHash string) (*Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	s, ok := fs.data.Sessions[idHash]
	if !ok {
		return nil, nil
	}
	delete(fs.data.Sessions, idHash)
	if err := fs.commit(func() { fs.data.Sessions[idHash] = s }); err != nil {
		return nil, err
	}
	return &s, nil
}

func (fs *FileStore) AccountSessions(_ context.Context) ([]Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var out []Session
	for _, s := range fs.data.Sessions {
		if s.Account {
			out = append(out, s)
		}
	}
	return out, nil
}

func (fs *FileStore) DeleteSessionsBySub(_ context.Context, subs map[string]BlockEntry) ([]Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var gone []Session
	for id, s := range fs.data.Sessions {
		if _, ok := subs[s.Identity.Sub]; ok && !s.Account {
			gone = append(gone, s)
			delete(fs.data.Sessions, id)
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	if err := fs.commit(func() {
		for _, s := range gone {
			fs.data.Sessions[s.IDHash] = s
		}
	}); err != nil {
		return nil, err
	}
	return gone, nil
}

func (fs *FileStore) DeleteSessionsBySID(_ context.Context, sid string) ([]Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var gone []Session
	for id, s := range fs.data.Sessions {
		if sid != "" && s.SID == sid {
			gone = append(gone, s)
			delete(fs.data.Sessions, id)
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	if err := fs.commit(func() {
		for _, s := range gone {
			fs.data.Sessions[s.IDHash] = s
		}
	}); err != nil {
		return nil, err
	}
	return gone, nil
}

func (fs *FileStore) BlocklistRevision(_ context.Context) (int64, bool, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.data.Blocklist.Revision, !fs.data.Blocklist.SyncedAt.IsZero(), nil
}

func (fs *FileStore) BlockedEntry(_ context.Context, sub string) (*BlockEntry, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	e, ok := fs.data.Blocklist.Entries[sub]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (fs *FileStore) SaveBlocklist(_ context.Context, b Blocklist) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	prev := fs.data.Blocklist
	if !prev.SyncedAt.IsZero() && b.Revision < prev.Revision {
		return ErrStaleBlocklist
	}
	fs.data.Blocklist = b
	return fs.commit(func() { fs.data.Blocklist = prev })
}

// ---------- 待写队列 ----------

// outbox 是写日志的持久队列：每次去写之前一定已落盘，写成了才删；重启接着写。
// 登录和请求从不等它：进队列只在锁里改内存，落盘在后台（锁外写文件、fsync），写不进去只在健康检查里报。
// 一样的请求、登录失败合成一行（次数加一），10 秒内同一键最多新起一行，两类各有每分钟的额度。
type outbox struct {
	path     string
	mu       sync.Mutex
	seq      int64
	items    []outboxItem
	inflight map[int64]bool
	wake     chan struct{}
	kick     chan struct{} // 兜底对账叫醒在退避里等着的写入
	dirty    chan struct{}
	saveErr  bool
	gen      int64         // 队列改过几次
	savedGen int64         // 最近一次落盘（成没成功都算）包含了前多少次改动
	savedCh  chan struct{} // 每落一次盘就关掉换新的，flush 在上面等

	recent   map[string]time.Time  // 合并键 → 这一键最近一行写出去的时间（暂存的那行算到点的时间）
	prunedAt time.Time             // recent 上次清理的时间
	budgets  map[string]*rowBudget // rowRequest / rowLoginFail → 每分钟额度
}

// rowBudget 是一类可合并日志行每分钟新增的额度，丢了多少给健康检查看。
type rowBudget struct {
	limit     *perMinute
	dropping  bool // 正在丢（只在开始丢时记一行 stderr）
	dropped   int64
	droppedAt time.Time
}

// outboxItem 是日志表的一行。「项目」不在这里：中台按钥匙填。
type outboxItem struct {
	ID     int64          `json:"id"`
	Merge  string         `json:"merge,omitempty"`
	Fields map[string]any `json:"fields"`
	At     time.Time      `json:"at"`
	Hold   time.Time      `json:"hold"` // 同一键 10 秒内已写出过一行：到这个时间才写，期间再来的并进来
}

func openOutbox(path string) (*outbox, error) {
	ob := &outbox{path: path, items: []outboxItem{}, inflight: map[int64]bool{}, wake: make(chan struct{}, 1), kick: make(chan struct{}, 1),
		dirty: make(chan struct{}, 1), savedCh: make(chan struct{}), recent: map[string]time.Time{},
		budgets: map[string]*rowBudget{rowRequest: {limit: newPerMinute(requestRowsPerMinute)}, rowLoginFail: {limit: newPerMinute(loginFailRowsPerMinute)}}}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("sso: SSO_OUTBOX_FILE: %w", err)
	}
	if len(raw) > 0 {
		// v5 的队列每条多一个 target：log 是日志行，accounts 是账号表写回。
		var saved []struct {
			outboxItem
			Target string `json:"target"`
		}
		if json.Unmarshal(raw, &saved) != nil {
			// 文件坏了也不能挡登录：挪到一边留着查，从空队列开始。
			aside := path + ".corrupt-" + time.Now().Format("20060102T150405")
			os.Rename(path, aside)
			log.Printf("sso: outbox file unreadable, moved to %s", aside)
			saved = nil
		}
		dropped := 0
		for _, it := range saved {
			// v5 留下的账号表写回不要了：「状态」由中台算、中台对账补写。v5 的日志行带着「项目」，改由中台填。
			if it.Target != "" && it.Target != "log" {
				dropped++
				continue
			}
			delete(it.Fields, "项目")
			ob.items = append(ob.items, it.outboxItem)
			ob.seq = max(ob.seq, it.ID)
		}
		if dropped > 0 {
			log.Printf("sso: outbox dropped %d accounts-table writes left by v5 (the hub keeps the status columns now)", dropped)
		}
	}
	if err := ob.saveNow(); err != nil {
		return nil, fmt.Errorf("sso: SSO_OUTBOX_FILE: %w", err)
	}
	ob.wake <- struct{}{} // 重启前没写完的接着写
	return ob, nil
}

// kickBacklog 让还有积压的写入不等退避、马上再写一次（兜底对账每轮调一次）。
func (ob *outbox) kickBacklog() {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	if len(ob.items) > 0 {
		select {
		case ob.kick <- struct{}{}:
		default:
		}
	}
}

// changed 记一次改动并叫后台落盘。调用方持有 mu。
func (ob *outbox) changed() {
	ob.gen++
	select {
	case ob.dirty <- struct{}{}:
	default:
	}
}

func (ob *outbox) add(it outboxItem) {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	if !ob.merge(it) {
		if it.Merge != "" && !ob.admit(&it) {
			return
		}
		ob.seq++
		it.ID, it.At = ob.seq, time.Now()
		ob.items = append(ob.items, it)
		ob.trim()
	}
	ob.changed()
	select {
	case ob.wake <- struct{}{}:
	default:
	}
}

// merge 把这一行并进队列里还没写出去的一样的日志行：次数加一、耗时取最大，时间留第一次的。
func (ob *outbox) merge(it outboxItem) bool {
	if it.Merge == "" {
		return false
	}
	for i := range ob.items {
		e := &ob.items[i]
		if ob.inflight[e.ID] || e.Merge != it.Merge {
			continue
		}
		e.Fields["次数"] = asInt(e.Fields["次数"]) + 1
		if _, ok := it.Fields["耗时毫秒"]; ok {
			e.Fields["耗时毫秒"] = max(asInt(e.Fields["耗时毫秒"]), asInt(it.Fields["耗时毫秒"]))
		}
		return true
	}
	return false
}

// admit 决定一条可合并的日志能不能新起一行：这一类最近一分钟的额度用完就丢（开始丢时记一行 stderr，健康检查看得到丢了多少）；
// 同一键 10 秒内已写出过一行，这一行先暂存到窗口结束，期间再来的都并进它。调用方持有 mu。
func (ob *outbox) admit(it *outboxItem) bool {
	class, _, _ := strings.Cut(it.Merge, "\x00")
	b := ob.budgets[class]
	if b == nil {
		b = ob.budgets[rowRequest]
	}
	now := time.Now()
	if !b.limit.allow("") {
		if !b.dropping {
			log.Printf("sso: more than %d %s log rows in a minute, dropping the rest", b.limit.limit, class)
			b.dropping = true
		}
		b.dropped++
		b.droppedAt = now
		return false
	}
	b.dropping = false
	if now.Sub(ob.prunedAt) > logMergeWindow {
		for k, t := range ob.recent {
			if now.Sub(t) > logMergeWindow {
				delete(ob.recent, k)
			}
		}
		ob.prunedAt = now
	}
	sendAt := now
	if last, ok := ob.recent[it.Merge]; ok && now.Sub(last) < logMergeWindow {
		sendAt = last.Add(logMergeWindow)
		it.Hold = sendAt
	}
	ob.recent[it.Merge] = sendAt
	return true
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// trim 超过上限时丢最旧的（正在写的不丢），并记 stderr。
func (ob *outbox) trim() {
	drop := len(ob.items) - outboxMaxLogs
	if drop <= 0 {
		return
	}
	dropped := 0
	ob.items = slices.DeleteFunc(ob.items, func(it outboxItem) bool {
		if dropped < drop && !ob.inflight[it.ID] {
			dropped++
			return true
		}
		return false
	})
	log.Printf("sso: outbox over %d log rows, dropped the %d oldest", outboxMaxLogs, dropped)
}

// saveLoop 有改动就在后台落盘；落盘时新来的改动攒着，下一次一起落。
func (ob *outbox) saveLoop() {
	for range ob.dirty {
		ob.saveNow()
	}
}

// saveNow 在锁里拍一份快照，放了锁再写临时文件、fsync、改名；只在刚开始失败时记一行 stderr。只有 saveLoop（和启动时）调它，同一时间只有一个在写。
func (ob *outbox) saveNow() error {
	ob.mu.Lock()
	raw, _ := json.Marshal(ob.items)
	gen := ob.gen
	ob.mu.Unlock()
	err := writeSynced(ob.path, raw)
	ob.mu.Lock()
	failedBefore := ob.saveErr
	ob.saveErr = err != nil
	ob.savedGen = max(ob.savedGen, gen)
	close(ob.savedCh)
	ob.savedCh = make(chan struct{})
	ob.mu.Unlock()
	if err != nil && !failedBefore {
		log.Printf("sso: outbox write failed (kept in memory): %v", err)
	}
	return err
}

func writeSynced(path string, raw []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		_, err = f.Write(raw)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	return err
}

// flush 等到此刻之前的改动都落过一次盘（写不进去也算落过：只在健康检查里报，不挡写出去）。
func (ob *outbox) flush(ctx context.Context) error {
	ob.mu.Lock()
	want := ob.gen
	ob.mu.Unlock()
	for {
		ob.mu.Lock()
		if ob.savedGen >= want {
			ob.mu.Unlock()
			return nil
		}
		ch := ob.savedCh
		select {
		case ob.dirty <- struct{}{}:
		default:
		}
		ob.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// take 取一批（最多 500 条）去写；还没到点的暂存行不取，返回最早到点的时间（没有是零值）。
func (ob *outbox) take() ([]outboxItem, time.Time) {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	now := time.Now()
	var batch []outboxItem
	var next time.Time
	for _, it := range ob.items {
		if ob.inflight[it.ID] {
			continue
		}
		if it.Hold.After(now) {
			if next.IsZero() || it.Hold.Before(next) {
				next = it.Hold
			}
			continue
		}
		ob.inflight[it.ID] = true
		batch = append(batch, outboxItem{ID: it.ID, Merge: it.Merge, Fields: maps.Clone(it.Fields), At: it.At})
		if len(batch) == outboxBatch {
			break
		}
	}
	return batch, next
}

func (ob *outbox) finish(batch []outboxItem, delivered bool) {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	done := map[int64]bool{}
	for _, it := range batch {
		delete(ob.inflight, it.ID)
		done[it.ID] = true
	}
	if !delivered {
		return
	}
	ob.items = slices.DeleteFunc(ob.items, func(it outboxItem) bool { return done[it.ID] })
	ob.changed()
}

type outboxStats struct {
	pending    int
	oldest     time.Time
	saveFailed bool
	dropped    map[string]int64     // rowRequest / rowLoginFail → 丢了多少行
	droppedAt  map[string]time.Time // 最近一次丢的时间
}

func (ob *outbox) stats() outboxStats {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	st := outboxStats{pending: len(ob.items), saveFailed: ob.saveErr, dropped: map[string]int64{}, droppedAt: map[string]time.Time{}}
	for class, b := range ob.budgets {
		st.dropped[class], st.droppedAt[class] = b.dropped, b.droppedAt
	}
	for _, it := range ob.items {
		if st.oldest.IsZero() || it.At.Before(st.oldest) {
			st.oldest = it.At
		}
	}
	return st
}

// outboxWorker 有东西进来就写，写的时候新进来的攒着下一次一起写；暂存的行到点再写；
// 失败才按 1 秒起翻倍、最长 5 分钟退避重试（中台暂时不可用、日志表写满了都一样，登录和请求照常）。
// 启动时和每写 500 行查一次日志表总行数，快满了健康检查提醒；表里的行永远不删。
func (s *SSO) outboxWorker() {
	backoff := time.Second
	written := 0
	s.countLogTable()
	for {
		batch, next := s.outbox.take()
		if len(batch) == 0 {
			if next.IsZero() {
				<-s.outbox.wake
			} else {
				select {
				case <-s.outbox.wake:
				case <-time.After(time.Until(next)):
				}
			}
			continue
		}
		// 写出去之前一定已在盘上：落盘在后台，这里等它落完这批。
		fctx, fcancel := context.WithTimeout(context.Background(), upstreamLimit)
		if err := s.outbox.flush(fctx); err != nil {
			log.Printf("sso: outbox flush: %v", err)
		}
		fcancel()
		err := s.deliver(batch)
		s.outbox.finish(batch, err == nil)
		if err != nil {
			log.Printf("sso: outbox: %d not written, retry in %s: %v", len(batch), backoff, err)
			select {
			case <-time.After(backoff):
			case <-s.outbox.kick:
			}
			backoff = min(backoff*2, outboxMaxBackoff)
			continue
		}
		backoff = time.Second
		if written += len(batch); written >= logCountEvery {
			written = 0
			s.countLogTable()
		}
	}
}

// deliver 把一批日志行交给中台写进共用的日志表（「项目」由中台按钥匙填）。
func (s *SSO) deliver(batch []outboxItem) error {
	rows := make([]map[string]any, len(batch))
	for i, it := range batch {
		rows[i] = map[string]any{"fields": it.Fields}
	}
	hc := s.callHub(http.MethodPost, "/log", map[string]any{"records": rows}, hubTimeout)
	switch hc.kind {
	case hubOK:
		return nil
	case hubRevoked:
		return errors.New("hub refused log rows")
	}
	return errors.New(hc.why)
}

// countLogTable 读日志表总行数（不分项目，日志表是共用的），到 SSO_LOG_WARN_ROWS 就让健康检查带 warn_log_table_near_full。
// 失败只记 stderr，沿用上一次的结论。
func (s *SSO) countLogTable() {
	hc := s.callHub(http.MethodGet, "/log/total", nil, hubTimeout)
	total, ok := hc.body["total"].(float64)
	if !ok {
		why := hc.why
		switch hc.kind {
		case hubOK:
			why = "malformed reply"
		case hubRevoked:
			why = "refused"
		}
		log.Printf("sso: log table count: %s", why)
		return
	}
	near := total >= float64(s.logWarnRows)
	s.hubMu.Lock()
	s.logNearFull = near
	s.hubMu.Unlock()
	if near {
		log.Printf("sso: log table has %.0f rows (SSO_LOG_WARN_ROWS %d): expand the table in Feishu, then raise SSO_LOG_WARN_ROWS", total, s.logWarnRows)
	}
}
