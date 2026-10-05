import { lazyView } from '../../utils/lazyView';

// The evidence sheet carries chart.js; it loads on demand, warmed when idle.
export const EvidenceSheet = lazyView(() =>
  import('./EvidenceSheet').then((m) => ({ default: m.EvidenceSheet })),
);
