function normalizeWeight(value) {
  const weight = Number(value);
  return Number.isFinite(weight) && weight > 0 ? weight : 1;
}

function normalizeProgress(done, total) {
  const normalizedTotal = Number(total);
  const normalizedDone = Number(done);
  if (!Number.isFinite(normalizedTotal) || normalizedTotal <= 0) return null;
  if (!Number.isFinite(normalizedDone)) return null;
  return Math.max(0, Math.min(1, normalizedDone / normalizedTotal));
}

/**
 * Aggregate verifiable workflow units into one total import percentage.
 *
 * Each phase only advances from an explicit done/total pair or completePhase().
 * The aggregate is capped at 99 until every required phase is complete and the
 * caller confirms that preview + copy state were synchronously committed.
 */
export function createImportProgressTracker({
  phases = [],
  onChange = () => {},
} = {}) {
  if (!Array.isArray(phases) || phases.length === 0) {
    throw new TypeError('import progress requires at least one phase');
  }

  const phaseStates = new Map();
  phases.forEach((phase) => {
    const id = String(phase?.id || '').trim();
    if (!id) throw new TypeError('import progress phase id is required');
    if (phaseStates.has(id)) throw new TypeError(`duplicate import progress phase: ${id}`);
    phaseStates.set(id, {
      id,
      weight: normalizeWeight(phase?.weight),
      required: phase?.required !== false,
      ratio: 0,
      done: 0,
      total: null,
    });
  });

  const totalWeight = Array.from(phaseStates.values())
    .reduce((sum, phase) => sum + phase.weight, 0);
  let status = '';
  let activePhase = '';
  let cancelled = false;
  let cancelReason = '';
  let committed = false;
  let lastPercent = 0;

  function calculatePercent() {
    if (committed) return 100;
    const completedWeight = Array.from(phaseStates.values())
      .reduce((sum, phase) => sum + (phase.weight * phase.ratio), 0);
    return Math.min(99, Math.max(lastPercent, Math.round((completedWeight / totalWeight) * 100)));
  }

  function snapshot() {
    return {
      percent: calculatePercent(),
      status,
      phase: activePhase,
      visible: true,
      cancelled,
      cancelReason,
      committed,
    };
  }

  function emit() {
    const next = snapshot();
    lastPercent = next.percent;
    onChange(next);
    return next;
  }

  function updateStatus(id, options) {
    activePhase = id;
    const nextStatus = options?.status ?? options?.text;
    if (typeof nextStatus === 'string' && nextStatus.trim()) status = nextStatus.trim();
  }

  function updatePhase(id, done, total, options = {}) {
    if (cancelled || committed) return snapshot();
    const phase = phaseStates.get(String(id));
    if (!phase) throw new TypeError(`unknown import progress phase: ${id}`);
    updateStatus(phase.id, options);
    const ratio = normalizeProgress(done, total);
    if (ratio !== null) {
      phase.ratio = Math.max(phase.ratio, ratio);
      phase.done = Math.max(phase.done, Math.max(0, Number(done)));
      phase.total = Number(total);
    }
    return emit();
  }

  function completePhase(id, options = {}) {
    if (cancelled || committed) return snapshot();
    const phase = phaseStates.get(String(id));
    if (!phase) throw new TypeError(`unknown import progress phase: ${id}`);
    updateStatus(phase.id, options);
    phase.ratio = 1;
    phase.total = phase.total ?? 1;
    phase.done = phase.total;
    return emit();
  }

  function cancel(reason = 'cancelled') {
    if (cancelled || committed) return snapshot();
    cancelled = true;
    cancelReason = String(reason || 'cancelled');
    return emit();
  }

  function commit(options = {}) {
    if (committed) return true;
    if (cancelled) return false;
    const ready = Array.from(phaseStates.values())
      .every(phase => !phase.required || phase.ratio >= 1);
    if (!ready) return false;
    const nextStatus = options?.status ?? options?.text;
    if (typeof nextStatus === 'string' && nextStatus.trim()) status = nextStatus.trim();
    committed = true;
    lastPercent = 100;
    emit();
    return true;
  }

  return {
    updatePhase,
    completePhase,
    snapshot,
    cancel,
    commit,
  };
}

export function bindImportProgressDom({ root, fill, text, percent } = {}) {
  return function renderImportProgress(snapshot = {}) {
    if (!root) return;
    const value = Math.max(0, Math.min(100, Math.round(Number(snapshot.percent) || 0)));
    const visible = snapshot.visible !== false;
    root.style.display = visible ? 'block' : 'none';
    root.dataset.totalProgress = visible ? 'true' : 'false';
    root.setAttribute('role', 'progressbar');
    root.setAttribute('aria-valuemin', '0');
    root.setAttribute('aria-valuemax', '100');
    root.setAttribute('aria-valuenow', String(value));
    root.classList.remove('is-indeterminate');
    root.classList.toggle('is-complete', value === 100 && snapshot.committed === true);
    if (fill) fill.style.width = `${value}%`;
    if (text && typeof snapshot.status === 'string') text.textContent = snapshot.status;
    if (percent) percent.textContent = `${value}%`;
  };
}
