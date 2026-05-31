// footer-segments-defaults.js — 页脚库前端【降级常量】（服务端 /api/rules 无响应时兜底）。
//
// 真相以服务端 /api/rules 的 config.modules.fbif_footer 为准（规则后台可编辑）；
// 这里只是离线/接口异常时的 fallback，与后端 api/rule_defaults.go defaultFBIFFooterModule()
// 保持一致。结构对齐服务端：segments[key] = { html_lines: [...] }。

const DECOR = 'https://static.foodtalks.cn/image/post/07079a66940e1ff57e71b140e7e36357.webp';
const GROUP_QR = 'https://static.foodtalks.cn/image/news/6e309987660da9af009b89be08c4aa74ed78.png';
const ACCOUNT_QR = 'https://static.foodtalks.cn/image/post/b5b448f324a538209eb53379bef8dea9a7bf.jpg';
const LOGO = 'https://static.foodtalks.cn/uploads/images/202007/e155b7852c10a72f7b73157fdf9c4bf0.png';

export const FOOTER_SEGMENT_ORDER_DEFAULT = ['biz_coop', 'join_group', 'follow_account', 'logo', 'more_info'];

export const FOOTER_SEGMENTS_DEFAULT = {
  biz_coop: { html_lines: [
    '<p style="text-align: left;"><em><span style="color: #7f7f7f; font-size: 14px;">*资讯投稿与商务合作，请查看&ldquo;<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/news/30752" target="_blank" rel="noopener">FoodTalks商务合作方案</a>&rdquo;，并联系Bobo。</span></em></p>',
  ] },
  join_group: { html_lines: [
    `<p style="text-align: center;"><img style="display: block; margin-left: auto; margin-right: auto;" src="${DECOR}" alt="" width="600" /></p>`,
    '<p style="color: #01396a; font-size: 20px;"><strong>加入微信群</strong></p>',
    '<p><br /></p>',
    `<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="${GROUP_QR}" alt="00" width="600" height="267" /></p>`,
    '<p style="text-align: left;"><em><span style="color: #7f7f7f; font-size: 14px;">*如遇二维码失效无法加群，请联系工作人员Lemon（微信号：FBIF_Lemon）。</span></em></p>',
  ] },
  follow_account: { html_lines: [
    '<p style="color: #01396a; font-size: 20px;"><strong>关注公众号</strong></p>',
    '<p><br /></p>',
    `<p><img class="mc-img" style="display: block; margin-left: auto; margin-right: auto;" src="${ACCOUNT_QR}" alt="" width="600" height="221" /></p>`,
  ] },
  logo: { html_lines: [
    `<p><img style="max-width: 100%; height: 27px; width: 126px;" src="${LOGO}" alt="FoodTalks logo" width="130" height="27" border="0" /></p>`,
  ] },
  more_info: { html_lines: [
    '<p>点击获取更多<a style="text-decoration: underline; color: #105b89;" href="https://www.foodtalks.cn/" target="_blank" rel="noopener">全球食品资讯</a></p>',
  ] },
};

export const FOOTER_PRESETS_DEFAULT = [
  { id: 'fbif-original', name: 'FBIF原创完整页脚', segments: { tip: true, biz_coop: true, join_group: true, follow_account: true, logo: true, more_info: true } },
  { id: 'repost-simple', name: '转载简版（提示+商务）', segments: { tip: true, biz_coop: true, join_group: false, follow_account: false, logo: false, more_info: false } },
  { id: 'tip-only', name: '仅提示声明', segments: { tip: true, biz_coop: false, join_group: false, follow_account: false, logo: false, more_info: false } },
  { id: 'none', name: '无页脚', segments: { tip: false, biz_coop: false, join_group: false, follow_account: false, logo: false, more_info: false } },
];
