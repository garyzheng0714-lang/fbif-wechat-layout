// copy-session.js — 复制链路的竞态守卫（架构体检 S2）。
//
// 背景：复制层（_articleCopy/_footerCopy）在渲染后仍被两路异步任务持续改写
// （图片上传重写、更多文章合成）。此前用两个裸计数器（_footerCopyGen、
// _moreArticlesCopySeq）+ 手工数字比较防「慢速旧任务覆盖新文章数据」，且每个
// 文章级重置点都要记得【成对】递增两个计数器——漏一个就是一次跨文章污染事故
// （2026-06 曾发生，commit 66535c8）。
//
// 现在：
//   - createVersionGate：版本门。异步任务开始时 capture()，回写前问 stale()；
//     invalidate() 作废所有在途 capture。语义自解释，杜绝裸数字比较。
//   - createCopySession：把「一次导入 = 一个会话」封装为唯一重置入口
//     resetForNewArticle()——内部作废全部 gate，调用方不可能再漏。
//
// 三个 gate 语义不同（不能合并为一个计数器）：
//   footerCopyGate    = _footerCopy 的写权版本（文章重置 + 更多文章权威重建时作废）
//   moreArticlesGate  = 更多文章合成任务的版本（文章重置 + 新一轮 prepare 时作废）
//   moreArticlesPreviewGate = 更多文章预览更新版本（后一次编辑必须淘汰前一次异步渲染）
// 契约测试：tests/copy-session.test.mjs。

export function createVersionGate(name) {
  let version = 0;
  return {
    name,
    invalidate() { version++; },
    // 捕获当前版本；stale() 为 true 表示捕获后发生过 invalidate，回写必须放弃。
    capture() {
      const v = version;
      return { stale: () => v !== version };
    },
  };
}

export function createCopySession() {
  const footerCopyGate = createVersionGate('footerCopy');
  const moreArticlesGate = createVersionGate('moreArticles');
  const moreArticlesPreviewGate = createVersionGate('moreArticlesPreview');
  return {
    footerCopyGate,
    moreArticlesGate,
    moreArticlesPreviewGate,
    // 文章级重置（新文章导入 / goBack / 快照恢复 / 纯文字重置）唯一入口：
    // 所有在途异步回写全部作废。新增 gate 时在此登记，调用方零改动。
    resetForNewArticle() {
      footerCopyGate.invalidate();
      moreArticlesGate.invalidate();
      moreArticlesPreviewGate.invalidate();
    },
  };
}
