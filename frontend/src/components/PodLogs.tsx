import LogViewer from './logs/LogViewer';

interface PodLogsProps {
  cluster: string;
  namespace: string;
  name: string;
  containers?: any[];
  initContainers?: any[];
}

const PodLogs = ({ cluster, namespace, name, containers, initContainers }: PodLogsProps) => (
  <LogViewer
    cluster={cluster}
    namespace={namespace}
    name={name}
    kind="pod"
    containers={[
      ...(containers || []).map((c: any) => ({ name: c.name, init: false })),
      ...(initContainers || []).map((c: any) => ({ name: c.name, init: true })),
    ]}
  />
);

export default PodLogs;
