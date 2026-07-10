// render-service 测试样例 Block[]（单一真相源：golden 生成脚本与测试文件共用，防两处漂移）。
// 块契约 = api/wechat_blocks.go（/api/fetch-article、/api/html-to-blocks 的输出）：
// h/cap 读 text；txt/bq/li 读 runs；img 读 src/dataRatio；table 读 rows[][]{runs,header}。
// 覆盖面刻意求全：作者行（网站署名规则）、外链+公众号链（渠道链接策略分叉点）、图片、
// 图注、引用、表格（CMS 存活开关的落点）——golden 因此能锁住整条渲染管线的字节。
export const SAMPLE_BLOCKS = [
  { k: 'txt', runs: [{ type: 'txt', text: '作者：张三' }] },
  { k: 'txt', runs: [{ type: 'txt', text: '雀巢宣布收购某植物基品牌，交易金额未披露。' }] },
  { k: 'h', text: '交易细节' },
  {
    k: 'txt',
    runs: [
      { type: 'txt', text: '据' },
      { type: 'link', text: '外部报道', href: 'https://example.com/news/1' },
      { type: 'txt', text: '与' },
      { type: 'link', text: '公众号旧文', href: 'https://mp.weixin.qq.com/s/abc123' },
      { type: 'txt', text: '，双方已达成一致。' },
    ],
  },
  { k: 'img', src: 'https://mmbiz.qpic.cn/mmbiz_jpg/fake/640.jpg', dataW: '640', dataRatio: '0.56' },
  { k: 'cap', text: '图片来源：品牌官网' },
  { k: 'bq', runs: [{ type: 'txt', text: '「这是一次战略性收购。」' }] },
  {
    k: 'table',
    rows: [
      [
        { runs: [{ type: 'txt', text: '品类' }], header: true },
        { runs: [{ type: 'txt', text: '份额' }], header: true },
      ],
      [
        { runs: [{ type: 'txt', text: '植物基饮料' }] },
        { runs: [{ type: 'txt', text: '23%' }] },
      ],
    ],
  },
];

export const SAMPLE_ARTICLE = {
  title: '雀巢收购植物基品牌',
  source: 'https://mp.weixin.qq.com/s/sample-source',
  blocks: SAMPLE_BLOCKS,
  isRepost: true,
};
