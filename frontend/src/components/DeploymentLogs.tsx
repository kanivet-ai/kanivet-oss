import LogViewer from './logs/LogViewer';

interface DeploymentLogsProps {
  cluster: string;
  namespace: string;
  name: string;
  resourceType?: string;
}

const KINDS = ['deployment', 'statefulset', 'daemonset', 'replicaset', 'job'];

const normalizeKind = (k?: string) => {
  const s = (k || 'deployment').toLowerCase();
  if (KINDS.includes(s)) return s;
  const singular = s.replace(/s$/, '');
  return KINDS.includes(singular) ? singular : 'deployment';
};

const DeploymentLogs = ({ cluster, namespace, name, resourceType }: DeploymentLogsProps) => (
  <LogViewer
    cluster={cluster}
    namespace={namespace}
    name={name}
    kind={normalizeKind(resourceType)}
  />
);

export default DeploymentLogs;
