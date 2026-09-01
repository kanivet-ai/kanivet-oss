import ScrollContainer from './ScrollContainer';
import TabContent from './common/TabContent';
import PodLogs from './PodLogs';
import DeploymentLogs from './DeploymentLogs';
import PodShell from './PodShell';
import NodeShell from './NodeShell';
import TerminalContainer from './TerminalContainer';
import YamlEditor from './YamlEditor';
import CrossplaneTrace from './CrossplaneTrace';

interface BottomTabContentProps {
  tab: any;
}

export function BottomTabContent({ tab }: BottomTabContentProps) {
  let content = null;

  if (tab.type === 'logs') {
    content = (
      <ScrollContainer
        className="resource-list-tab-body"
        viewportClassName="resource-list-tab-viewport"
      >
        <PodLogs
          cluster={tab.cluster}
          namespace={tab.resource.metadata?.namespace || 'default'}
          name={tab.resource.metadata?.name || ''}
          containers={tab.resource.spec?.containers}
          initContainers={tab.resource.spec?.initContainers}
        />
      </ScrollContainer>
    );
  } else if (tab.type === 'deployment-logs') {
    content = (
      <ScrollContainer
        className="resource-list-tab-body"
        viewportClassName="resource-list-tab-viewport"
      >
        <DeploymentLogs
          cluster={tab.cluster}
          namespace={tab.resource.metadata?.namespace || 'default'}
          name={tab.resource.metadata?.name || ''}
          resourceType={tab.resource.kind}
        />
      </ScrollContainer>
    );
  } else if (tab.type === 'shell' && tab.resource.kind === 'Pod') {
    content = (
      <PodShell
        cluster={tab.cluster}
        namespace={tab.resource.metadata?.namespace || 'default'}
        podName={tab.resource.metadata?.name || ''}
        containers={tab.resource.spec?.containers}
        initContainers={tab.resource.spec?.initContainers}
        containerStatuses={tab.resource.status?.containerStatuses}
        initContainerStatuses={tab.resource.status?.initContainerStatuses}
        tabId={tab.id}
        initialContainer={tab.selectedContainer}
      />
    );
  } else if (tab.type === 'shell' && tab.resource.kind === 'Node') {
    content = (
      <NodeShell
        cluster={tab.cluster}
        nodeName={tab.resource.metadata?.name || ''}
      />
    );
  } else if (tab.type === 'shell' && tab.resource.kind === 'Terminal') {
    content = <TerminalContainer initialTabId={tab.id} />;
  } else if (tab.type === 'edit' || tab.type === 'create') {
    content = (
      <YamlEditor
        resource={tab.resource}
        cluster={tab.cluster}
        mode={tab.type === 'create' ? 'create' : 'edit'}
      />
    );
  } else if (tab.type === 'trace') {
    const apiVersion = tab.resource.apiVersion || '';
    const parts = apiVersion.split('/');
    const group = parts.length > 1 ? parts[0] : '';
    const version = parts.length > 1 ? parts[1] : apiVersion;

    content = (
      <ScrollContainer
        className="resource-list-tab-body"
        viewportClassName="resource-list-tab-viewport"
      >
        <CrossplaneTrace
          cluster={tab.cluster}
          group={group}
          version={version}
          kind={tab.resource.kind || ''}
          namespace={tab.resource.metadata?.namespace}
          name={tab.resource.metadata?.name || ''}
        />
      </ScrollContainer>
    );
  }

  return content ? (
    <div className={`resource-list-bottom-tab-content resource-list-bottom-tab-${tab.type}`}>
      {content}
    </div>
  ) : null;
}

interface DetailTabContentProps {
  tab: any;
}

export function DetailTabContent({ tab }: DetailTabContentProps) {
  return <TabContent tab={tab} mode="center" isDeleted={tab.isDeleted} />;
}
