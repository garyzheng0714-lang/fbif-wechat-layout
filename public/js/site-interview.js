// FoodTalks 网站访谈问答识别。
// 同一篇文章里两个及以上角色前缀反复出现，才把「角色：」中的角色标成网站语义样式；
// 偶发的「提示：」「背景：」和正文小标签不会触发。

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

export function markInterviewSpeakerPrefixes(elems, opts = {}) {
  const minRepeats = Number(opts.minRepeats) > 1 ? Number(opts.minRepeats) : 3;
  const counts = new Map();

  for (const elem of elems || []) {
    if (!elem || elem.k !== 'p') continue;
    const label = speakerLabel(elem.runs);
    if (label) counts.set(label, (counts.get(label) || 0) + 1);
  }

  const speakers = new Set(
    Array.from(counts.entries())
      .filter(([, count]) => count >= minRepeats)
      .map(([label]) => label),
  );
  if (speakers.size < 2) return elems;

  return (elems || []).map(elem => {
    if (!elem || elem.k !== 'p') return elem;
    const label = speakerLabel(elem.runs);
    if (!speakers.has(label)) return elem;
    const kind = /^《[^》]+》$/.test(label) ? 'publication' : 'person';
    return {
      ...elem,
      runs: markTextPrefix(elem.runs, label.length, kind),
    };
  });
}
