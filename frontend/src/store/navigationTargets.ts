export const pageResources: Record<string, any> = {
  overview: { name: 'cluster-dashboard', group: '', version: 'v1', kind: 'ClusterDashboard', namespaced: false },
  finops: { name: 'finops-dashboard', group: '', version: 'v1', kind: 'FinOpsDashboard', namespaced: false },
  rightsizing: { name: 'rightsizing-dashboard', group: '', version: 'v1', kind: 'RightsizingDashboard', namespaced: false },
  certificates: { name: 'certificates-dashboard', group: '', version: 'v1', kind: 'CertificatesDashboard', namespaced: false },
  topology: { name: 'topology', group: '', version: 'v1', kind: 'TopologyView', namespaced: false },
  'incident-timeline': { name: 'incident-timeline', group: '', version: 'v1', kind: 'IncidentTimeline', namespaced: false },
  helm: { name: 'helm-releases', group: '', version: 'v1', kind: 'HelmReleases', namespaced: false },
  'argo-overview': { name: 'argo-applications-overview', group: 'argoproj.io', version: 'v1alpha1', kind: 'ArgoApplicationsOverview', namespaced: false },
  compare: { name: 'resource-compare', group: '', version: 'v1', kind: 'ResourceCompare', namespaced: false },
  'upgrade-readiness': { name: 'upgrade-readiness', group: '', version: 'v1', kind: 'UpgradeReadiness', namespaced: false },
  'cluster-settings': { name: 'cluster-settings', group: '', version: 'v1', kind: 'ClusterSettings', namespaced: false },
  access: { name: 'access-explorer', group: '', version: 'v1', kind: 'AccessExplorer', namespaced: false },
};

type PageNodeType =
  | 'overview'
  | 'finops'
  | 'rightsizing'
  | 'certificates'
  | 'compare'
  | 'incident-timeline'
  | 'helm'
  | 'argo-overview'
  | 'cluster-settings'
  | 'upgrade-readiness'
  | 'access'
  | 'topology';

/** The tree node of a tab that shows a page rather than a resource list. */
export function pageNode(kind: string, cluster: string | null) {
  const pages: Record<string, { id: string; label: string; type: PageNodeType }> = {
    ClusterSettings: { id: 'cluster-settings', label: 'Cluster settings', type: 'cluster-settings' },
    ClusterDashboard: { id: 'cluster-overview', label: 'Overview', type: 'overview' },
    FinOpsDashboard: { id: 'finops-dashboard', label: 'FinOps', type: 'finops' },
    RightsizingDashboard: { id: 'rightsizing-dashboard', label: 'Rightsizing', type: 'rightsizing' },
    CertificatesDashboard: { id: 'certificates-dashboard', label: 'Certificates', type: 'certificates' },
    TopologyView: { id: 'topology', label: 'Topology', type: 'topology' },
    IncidentTimeline: { id: 'incident-timeline', label: 'Incident Timeline', type: 'incident-timeline' },
    HelmReleases: { id: 'helm-releases', label: 'Helm Releases', type: 'helm' },
    ArgoApplicationsOverview: { id: 'argo-overview', label: 'Apps Overview', type: 'argo-overview' },
    ResourceCompare: { id: 'resource-compare', label: 'Compare', type: 'compare' },
    UpgradeReadiness: { id: 'upgrade-readiness', label: 'Upgrade readiness', type: 'upgrade-readiness' },
    AccessExplorer: { id: 'access-explorer', label: 'Access', type: 'access' },
  };
  const p = pages[kind];
  return p ? { ...p, data: { cluster } } : null;
}

