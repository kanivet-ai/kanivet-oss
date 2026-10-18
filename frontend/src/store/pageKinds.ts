// Resource-list tabs that show a full page rather than a resource table. They
// name no API resource, so nothing is subscribed for them.
export const PAGE_KINDS = new Set([
  'ClusterSettings',
  'ClusterDashboard',
  'FinOpsDashboard',
  'RightsizingDashboard',
  'HelmReleases',
  'IncidentTimeline',
  'ArgoApplicationsOverview',
]);
