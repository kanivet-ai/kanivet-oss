import { useStore } from '../../store';
import { pageResources } from '../../store/navigationTargets';

/* Command palette entries for the GitOps and CloudNativePG pages ("> gitops",
   "> postgres"). They open the page for the current cluster; the backend
   page shows an explanatory empty state when the tool is not installed. */

export const integrationCommands = [
  { id: 'open-gitops', name: 'open gitops (argo cd + flux)', description: 'Argo CD applications and Flux objects in one view', category: 'Commands' },
  { id: 'open-flux', name: 'open flux reconciliation status', description: 'Flux Kustomizations, Helm releases and sources', category: 'Commands' },
  { id: 'open-postgres', name: 'open postgres clusters (cloudnativepg)', description: 'CloudNativePG clusters, replication and backups', category: 'Commands' },
];

const PAGE_FOR: Record<string, { type: string; label: string; id: string }> = {
  'open-gitops': { type: 'gitops', label: 'GitOps', id: 'gitops-overview' },
  'open-flux': { type: 'gitops', label: 'GitOps', id: 'gitops-overview' },
  'open-postgres': { type: 'cnpg-overview', label: 'PostgreSQL clusters', id: 'cnpg-overview' },
};

/** Runs a palette command; false when the id is not one of ours. */
export function runIntegrationCommand(id: string): boolean {
  const page = PAGE_FOR[id];
  if (!page) return false;
  const state = useStore.getState();
  const cluster = state.currentTab;
  if (!cluster) return true;
  if (id === 'open-flux') {
    try {
      const key = `kanivet.gitopsPage.${cluster}`;
      const prev = JSON.parse(localStorage.getItem(key) || '{}');
      localStorage.setItem(key, JSON.stringify({ ...prev, tool: 'flux' }));
    } catch {}
  }
  state.selectNode({ id: page.id, label: page.label, type: page.type, data: { cluster } });
  void state.openResourceListTab(pageResources[page.type], cluster, false);
  void state.recordNavigation(page.type, page.id, { cluster }).catch(() => {});
  return true;
}
