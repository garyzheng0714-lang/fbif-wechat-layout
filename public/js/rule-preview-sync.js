// 规则配置写入引擎后的预览提交结果。宿主可以据此区分：
// 服务端规则已经保存，但预览已提交 / 延后 / 失败，避免把两件事混成一个“保存失败”。

export function requestRulePreviewSync(engineApi, triggerRerender) {
  if (!triggerRerender) return { status: 'not-requested' };
  if (!engineApi) return { status: 'unavailable' };
  if (typeof engineApi.rerenderWithStatus === 'function') {
    try {
      return Promise.resolve(engineApi.rerenderWithStatus()).catch(err => ({
        status: 'failed',
        error: String(err && err.message || err),
      }));
    } catch (err) {
      return { status: 'failed', error: String(err && err.message || err) };
    }
  }
  if (typeof engineApi.rerender !== 'function') return { status: 'unavailable' };
  try {
    return Promise.resolve(engineApi.rerender()).then(result => ({
      status: result === true ? 'committed' : (result === false ? 'failed' : 'noop'),
    })).catch(err => ({
      status: 'failed',
      error: String(err && err.message || err),
    }));
  } catch (err) {
    return { status: 'failed', error: String(err && err.message || err) };
  }
}

export function rulePreviewSavedMessage(result) {
  const status = result && result.status;
  if (status === 'deferred') return '规则已保存，当前文章排版完成后会自动应用';
  if (status === 'failed' || status === 'superseded' || status === 'unavailable') {
    return '规则已保存，但当前预览刷新失败；请重试刷新或重新导入文章';
  }
  return '已保存';
}

export function rulePreviewCommitted(result) {
  const status = result && result.status;
  return status === 'committed' || status === 'noop' || status === 'not-requested';
}
