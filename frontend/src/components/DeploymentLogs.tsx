import LogViewer from './logs/LogViewer';

interface DeploymentLogsProps {
  cluster: string;
  namespace: string;
  name: string;
  resourceType?: string;
  active?: boolean;
}

const KINDS = ['deployment', 'statefulset', 'daemonset', 'replicaset', 'job'];

const normalizeKind = (k?: string) => {
  const s = (k || 'deployment').toLowerCase();
  if (KINDS.includes(s)) return s;
  const singular = s.replace(/s$/, '');
  return KINDS.includes(singular) ? singular : 'deployment';
};

const DeploymentLogs = ({
  cluster,
  namespace,
  name,
  resourceType,
  active,
}: DeploymentLogsProps) => (
  <LogViewer
    cluster={cluster}
    namespace={namespace}
    name={name}
    kind={normalizeKind(resourceType)}
    active={active}
  />
);

export default DeploymentLogs;
