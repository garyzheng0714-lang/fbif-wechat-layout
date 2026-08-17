// FoodTalks 网站访谈问答识别。
// 判定「这是访谈」：同一篇文章里两个及以上角色前缀（「角色：」）各自反复出现
// （默认 ≥3 次），才把角色标成网站语义样式；偶发的「提示：」「背景：」和正文
// 小标签不会触发。
//
// 角色分工（2026-08-17 对齐排版规范 (6) 对话、问答或访谈）：
//   - 提问方 → FoodTalks 蓝并加粗（interviewSpeaker: 'interviewer'）
//   - 回答方 → 默认颜色并加粗（interviewSpeaker: 'interviewee'）
//   - 提问方前缀独立成段时与下一段提问内容合并（规范：提问方+提问内容间不空行）
// 提问方判定（确定性打分，不交模型）：
//   1. 《书名号》媒体名是强信号（《中国企业家》：…）；
//   2. 其段落以问号收尾的比例明显更高（先问后答，问句几乎都带问号）；
//   3. 打平时取全文首个出现的角色（访谈惯例先问后答）。
// 多角色圆桌同理：得分最高者是主持人（提问方），其余都是回答方。

function textOfRuns(runs) {
  return (runs || [])
    .filter(run => run && (run.type === 'txt' || run.type === 'link'))
    .map(run => String(run.text || ''))
    .join('');
}

function speakerLabel(runs) {
  const match = textOfRuns(runs).match(/^([^：:\n]{1,30})[：:]/);
  return match ? match[1].trim() : '';
}

function markTextPrefix(runs, length, kind) {
  let remaining = length;
  const out = [];
  for (const run of runs || []) {
    const text = String((run && run.text) || '');
    if (remaining <= 0 || !run || run.type !== 'txt' || !text) {
      out.push(run && { ...run });
      continue;
    }
    const take = Math.min(remaining, text.length);
    if (take > 0) {
      out.push({
        ...run,
        text: text.slice(0, take),
        interviewSpeaker: kind,
      });
      remaining -= take;
    }
    if (take < text.length) out.push({ ...run, text: text.slice(take) });
  }
  return out;
}

// pickInterviewer 从统计里选出提问方：书名号 > 问号率（差距 ≥0.25 才算显著）> 首个出现。
function pickInterviewer(stats) {
  const list = Array.from(stats.values());
  const pub = list.filter(s => /^《[^》]+》$/.test(s.label));
  if (pub.length === 1) return pub[0].label;
  const sorted = list.slice().sort((a, b) => (b.questions / b.count) - (a.questions / a.count));
  if (sorted.length >= 2 &&
      (sorted[0].questions / sorted[0].count) - (sorted[1].questions / sorted[1].count) >= 0.25) {
    return sorted[0].label;
  }
  return list.slice().sort((a, b) => a.firstIndex - b.firstIndex)[0].label;
}

export function markInterviewSpeakerPrefixes(elems, opts = {}) {
  const minRepeats = Number(opts.minRepeats) > 1 ? Number(opts.minRepeats) : 3;
  const stats = new Map();

  (elems || []).forEach((elem, index) => {
    if (!elem || elem.k !== 'p') return;
    const label = speakerLabel(elem.runs);
    if (!label) return;
    const entry = stats.get(label) || { label, count: 0, questions: 0, firstIndex: index };
    entry.count += 1;
    if (/[？?]\s*$/.test(textOfRuns(elem.runs).trim())) entry.questions += 1;
    stats.set(label, entry);
  });

  for (const [label, entry] of stats) {
    if (entry.count < minRepeats) stats.delete(label);
  }
  if (stats.size < 2) return elems;

  const interviewer = pickInterviewer(stats);
  const speakers = new Set(stats.keys());

  const out = [];
  const list = elems || [];
  for (let i = 0; i < list.length; i++) {
    let elem = list[i];
    if (!elem || elem.k !== 'p') {
      out.push(elem);
      continue;
    }
    let label = speakerLabel(elem.runs);
    // 提问方前缀独立成段（「TopDigital：」或「TopDigital」整段）→ 与下一段提问
    // 内容合并（规范：提问方+提问内容间不空行）。下一段自己是角色段则不合并。
    const bare = textOfRuns(elem.runs).trim().replace(/[：:]\s*$/, '');
    if (speakers.has(bare) && (label === bare || label === '')) {
      const next = list[i + 1];
      if (next && next.k === 'p' && !speakers.has(speakerLabel(next.runs))) {
        elem = {
          ...elem,
          runs: [
            { type: 'txt', text: bare + '：' },
            ...(next.runs || []).map(r => r && { ...r }),
          ],
        };
        label = bare;
        i += 1; // 吃掉被合并的内容段
      }
    }
    if (!speakers.has(label)) {
      out.push(elem);
      continue;
    }
    const kind = label === interviewer ? 'interviewer' : 'interviewee';
    out.push({
      ...elem,
      runs: markTextPrefix(elem.runs, label.length, kind),
    });
  }
  return out;
}
