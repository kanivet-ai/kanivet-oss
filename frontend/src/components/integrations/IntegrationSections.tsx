import { lazyView } from '../../utils/lazyView';

/* Inspector sections contributed by the Flux and CloudNativePG integrations.
   ResourceDetailView renders this once above the generic custom-resource
   body; it picks the section by API group and kind. */

const FluxDetailSection = lazyView(() => import('../gitops/FluxDetailSection'));
const CNPGClusterSection = lazyView(() => import('../cnpg/CNPGClusterSection'));

const groupOf = (apiVersion?: string) => ((apiVersion || '').includes('/') ? (apiVersion as string).split('/')[0] : '');

export const isFluxObject = (resource: any) => groupOf(resource?.apiVersion).endsWith('.toolkit.fluxcd.io');
export const isCNPGCluster = (resource: any) => resource?.kind === 'Cluster' && groupOf(resource?.apiVersion) === 'postgresql.cnpg.io';

export const IntegrationSections = ({ cluster, resource }: { cluster: string; resource: any }) => {
  if (!resource?.metadata?.name) return null;
  if (isFluxObject(resource)) return <FluxDetailSection cluster={cluster} resource={resource} />;
  if (isCNPGCluster(resource)) return <CNPGClusterSection cluster={cluster} resource={resource} />;
  return null;
};
