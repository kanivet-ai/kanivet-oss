import { useStore } from '../../store';
import { pageResources } from '../../store/navigationTargets';
import { SHOW_HISTORY_EVENT } from './InspectorViewSwitch';

/** Palette commands of the change history feature. */
export function runChangesCommand(id: 'open-changes' | 'show-history' | string): void {
  const state = useStore.getState();
  const cluster = state.currentTab;
  if (id === 'open-changes') {
    if (cluster) void state.openResourceListTab(pageResources.changes, cluster);
    return;
  }
  if (id === 'show-history') {
    window.dispatchEvent(new CustomEvent(SHOW_HISTORY_EVENT));
  }
}
