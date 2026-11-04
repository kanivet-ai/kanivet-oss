import React, { useEffect, useState } from 'react';

export type InspectorView = 'overview' | 'history';

/** Event the palette's "Show resource history" command dispatches. */
export const SHOW_HISTORY_EVENT = 'kanivet:show-resource-history';

/** Kinds without a meaningful history of their own. */
const NO_HISTORY = new Set(['Event', 'events', 'CustomResourceDefinition', 'APIResourceDefinition']);

export const supportsHistory = (kind: string | undefined) => !!kind && !NO_HISTORY.has(kind);

/**
 * The inspector's Overview / History view. It resets to Overview when another
 * object is shown and follows the palette's history command.
 */
export function useInspectorView(uid: string | undefined): [InspectorView, (v: InspectorView) => void] {
  const [view, setView] = useState<InspectorView>('overview');
  useEffect(() => { setView('overview'); }, [uid]);
  useEffect(() => {
    const onShow = () => setView('history');
    window.addEventListener(SHOW_HISTORY_EVENT, onShow);
    return () => window.removeEventListener(SHOW_HISTORY_EVENT, onShow);
  }, []);
  return [view, setView];
}

const InspectorViewSwitch: React.FC<{ view: InspectorView; onChange: (v: InspectorView) => void }> = ({ view, onChange }) => (
  <div className="chg-inspector-switch">
    <div className="ap-segmented" role="tablist" aria-label="Inspector view">
      <button type="button" role="tab" aria-selected={view === 'overview'} onClick={() => onChange('overview')}>Overview</button>
      <button type="button" role="tab" aria-selected={view === 'history'} onClick={() => onChange('history')}>History</button>
    </div>
  </div>
);

export default InspectorViewSwitch;
