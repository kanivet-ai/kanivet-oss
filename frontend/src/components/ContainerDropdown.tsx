import React, { useMemo, useState } from 'react';
import { ChevronRightIcon, ExclamationTriangleIcon, MagnifyingGlassIcon } from '@radix-ui/react-icons';
import * as Collapsible from '@radix-ui/react-collapsible';
import PropertyRow from './common/PropertyRow';
import ClipboardCopy from './common/ClipboardCopy';
import { Tooltip } from './common/Tooltip';
import './ContainerDropdown.css';

const ENV_PREVIEW_COUNT = 10;
const ENV_KEY_MAX = 36;

const truncateMiddle = (s: string, max = ENV_KEY_MAX): string => {
  if (s.length <= max) return s;
  const head = Math.ceil((max - 1) * 0.55);
  const tail = max - 1 - head;
  return `${s.slice(0, head)}…${s.slice(-tail)}`;
};

const EnvList: React.FC<{ envVars: Record<string, string> }> = ({ envVars }) => {
  const [expanded, setExpanded] = useState(false);
  const [filter, setFilter] = useState('');

  const entries = useMemo(() => Object.entries(envVars), [envVars]);
  const filtered = useMemo(() => {
    if (!filter.trim()) return entries;
    const q = filter.toLowerCase();
    return entries.filter(([k, v]) => k.toLowerCase().includes(q) || String(v).toLowerCase().includes(q));
  }, [entries, filter]);

  const showFilter = entries.length > ENV_PREVIEW_COUNT;
  const visible = expanded || filter ? filtered : filtered.slice(0, ENV_PREVIEW_COUNT);
  const hiddenCount = (filter ? filtered.length : entries.length) - visible.length;

  return (
    <div className="env-list">
      {showFilter && (
        <div className="env-filter">
          <MagnifyingGlassIcon className="env-filter-icon" />
          <input
            className="env-filter-input"
            placeholder={`Filter ${entries.length} variables…`}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          {filter && (
            <button className="env-filter-clear" onClick={() => setFilter('')} title="Clear filter">×</button>
          )}
        </div>
      )}
      {visible.map(([key, value]) => {
        const truncatedKey = truncateMiddle(key);
        const valStr = String(value);
        return (
          <div key={key} className="env-item">
            {truncatedKey === key ? (
              <span className="env-key">{key}</span>
            ) : (
              <Tooltip content={key} side="top" align="start">
                <span className="env-key">{truncatedKey}</span>
              </Tooltip>
            )}
            {valStr.length > 40 ? (
              <Tooltip content={valStr} side="top" align="end">
                <span className="env-value">{value}</span>
              </Tooltip>
            ) : (
              <span className="env-value">{value}</span>
            )}
            <ClipboardCopy text={`${key}=${value}`} />
          </div>
        );
      })}
      {hiddenCount > 0 && !filter && (
        <button className="env-show-more" onClick={() => setExpanded(true)}>
          Show all {entries.length} variables
        </button>
      )}
      {expanded && !filter && entries.length > ENV_PREVIEW_COUNT && (
        <button className="env-show-more" onClick={() => setExpanded(false)}>
          Collapse
        </button>
      )}
      {filter && filtered.length === 0 && (
        <div className="env-empty">No variables match “{filter}”</div>
      )}
    </div>
  );
};

interface ContainerDropdownProps {
  container: any;
  containerSpec: any;
  podName: string;
  namespace: string;
  cluster: string;
  portForwards: Record<string, any>;
  loadingPorts: Set<string>;
  onPortForward: (containerName: string, port: number) => Promise<void>;
  volumes?: any[];
  onVolumeClick?: (resourceKind: string, resourceName: string, namespace: string) => void;
}

const ResourceDisplay: React.FC<{ resources: Record<string, string> }> = ({ resources }) => (
  <div className="resource-display">
    {Object.entries(resources).map(([key, value]) => (
      <div key={key} className="resource-item">
        <span className="resource-key">{key}</span>
        <span className="resource-value">{value}</span>
      </div>
    ))}
  </div>
);

const CollapsibleRow: React.FC<{ title: string; children: React.ReactNode; defaultOpen?: boolean; variant?: 'default' | 'warning' | 'error' }> = ({ title, children, defaultOpen = false, variant = 'default' }) => {
  const [open, setOpen] = React.useState(defaultOpen);
  return (
    <Collapsible.Root open={open} onOpenChange={setOpen} className={`container-collapsible ${variant !== 'default' ? `collapsible-${variant}` : ''}`}>
      <Collapsible.Trigger className="container-collapsible-trigger">
        <ChevronRightIcon className={`collapsible-chevron ${open ? 'open' : ''}`} />
        <span>{title}</span>
      </Collapsible.Trigger>
      <Collapsible.Content className="container-collapsible-content">
        {children}
      </Collapsible.Content>
    </Collapsible.Root>
  );
};

// Helper function to explain exit codes
const getExitCodeExplanation = (exitCode: number, reason?: string): string | null => {
  if (reason === 'OOMKilled') return 'Container exceeded memory limit';
  if (reason === 'Completed') return 'Container finished successfully';
  
  switch (exitCode) {
    case 0: return 'Success';
    case 1: return 'General error';
    case 2: return 'Misuse of shell command';
    case 126: return 'Command not executable';
    case 127: return 'Command not found';
    case 128: return 'Invalid exit argument';
    case 130: return 'SIGINT (Ctrl+C)';
    case 137: return 'SIGKILL - Killed (likely OOM or force killed)';
    case 139: return 'SIGSEGV - Segmentation fault';
    case 143: return 'SIGTERM - Graceful termination';
    default:
      if (exitCode > 128 && exitCode < 256) {
        const signal = exitCode - 128;
        return `Killed by signal ${signal}`;
      }
      return null;
  }
};

// Helper function to format time since crash
const formatTimeSince = (timestamp: string): string => {
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  const diffMs = now - then;
  
  if (diffMs < 60000) return `${Math.round(diffMs / 1000)}s ago`;
  if (diffMs < 3600000) return `${Math.round(diffMs / 60000)}m ago`;
  if (diffMs < 86400000) return `${Math.round(diffMs / 3600000)}h ago`;
  return `${Math.round(diffMs / 86400000)}d ago`;
};

// Helper to shorten container ID for display
const shortenContainerId = (containerId: string): string => {
  // Format: containerd://abc123... or docker://abc123...
  const parts = containerId.split('://');
  if (parts.length === 2) {
    const runtime = parts[0];
    const id = parts[1].slice(0, 12);
    return `${runtime}://${id}...`;
  }
  return containerId.slice(0, 20) + '...';
};

// Component to display last termination status (OOMKilled, exit codes, etc.)
const LastTerminationStatus: React.FC<{ lastState: any }> = ({ lastState }) => {
  if (!lastState?.terminated) return null;
  
  const term = lastState.terminated;
  const isOOMKilled = term.reason === 'OOMKilled';
  const isError = term.exitCode !== 0 || isOOMKilled;
  const exitExplanation = getExitCodeExplanation(term.exitCode, term.reason);
  
  return (
    <CollapsibleRow 
      title={`Last Termination${isOOMKilled ? ' (OOMKilled)' : term.reason ? ` (${term.reason})` : ''}`} 
      defaultOpen={isError}
      variant={isOOMKilled ? 'error' : isError ? 'warning' : 'default'}
    >
      <div className="last-termination-details">
        {isOOMKilled && (
          <div className="termination-alert oom-killed">
            <ExclamationTriangleIcon />
            <span>Container was killed due to Out of Memory (OOMKilled)</span>
          </div>
        )}
        <div className="termination-info">
          {/* Time since crash - most important info at top */}
          {term.finishedAt && (
            <PropertyRow 
              label="Crashed" 
              value={formatTimeSince(term.finishedAt)} 
            />
          )}
          
          {/* Exit code with explanation */}
          <PropertyRow 
            label="Exit Code" 
            value={`${term.exitCode ?? 'N/A'}${exitExplanation ? ` (${exitExplanation})` : ''}`} 
          />
          
          {term.signal && term.signal !== 0 && (
            <PropertyRow label="Signal" value={term.signal.toString()} />
          )}
          {term.reason && !isOOMKilled && (
            <PropertyRow label="Reason" value={term.reason} />
          )}
          {term.message && (
            <PropertyRow label="Message" value={term.message} />
          )}
          
          {/* Duration the container ran */}
          {term.startedAt && term.finishedAt && (
            <PropertyRow 
              label="Ran for" 
              value={(() => {
                const start = new Date(term.startedAt).getTime();
                const end = new Date(term.finishedAt).getTime();
                const durationMs = end - start;
                if (durationMs < 1000) return `${durationMs}ms`;
                if (durationMs < 60000) return `${Math.round(durationMs / 1000)}s`;
                if (durationMs < 3600000) return `${Math.round(durationMs / 60000)}m`;
                if (durationMs < 86400000) return `${Math.round(durationMs / 3600000)}h`;
                return `${Math.round(durationMs / 86400000)}d`;
              })()}
            />
          )}
          
          {/* Container ID for debugging */}
          {term.containerID && (
            <PropertyRow 
              label="Container ID" 
              value={shortenContainerId(term.containerID)} 
              copyText={term.containerID}
              muted
              mono
            />
          )}
        </div>
      </div>
    </CollapsibleRow>
  );
};

// Component to display current termination status (for terminated containers)
const CurrentTerminationStatus: React.FC<{ state: any }> = ({ state }) => {
  if (!state?.terminated) return null;
  
  const term = state.terminated;
  const isOOMKilled = term.reason === 'OOMKilled';
  const isCompleted = term.reason === 'Completed';
  const exitExplanation = getExitCodeExplanation(term.exitCode, term.reason);
  
  // For completed containers, just show basic info
  if (isCompleted && term.exitCode === 0) {
    return (
      <div className="termination-info-inline">
        <PropertyRow label="Completed" value={term.finishedAt ? formatTimeSince(term.finishedAt) : 'N/A'} />
      </div>
    );
  }
  
  return (
    <div className="current-termination-details">
      {isOOMKilled && (
        <div className="termination-alert oom-killed">
          <ExclamationTriangleIcon />
          <span>Container terminated: Out of Memory (OOMKilled)</span>
        </div>
      )}
      {!isOOMKilled && term.exitCode !== 0 && (
        <div className="termination-alert error">
          <ExclamationTriangleIcon />
          <span>Container terminated with exit code {term.exitCode}{exitExplanation ? ` - ${exitExplanation}` : ''}</span>
        </div>
      )}
      <div className="termination-info">
        {/* Time since termination */}
        {term.finishedAt && (
          <PropertyRow label="Terminated" value={formatTimeSince(term.finishedAt)} />
        )}
        
        {/* Exit code with explanation */}
        <PropertyRow 
          label="Exit Code" 
          value={`${term.exitCode ?? 'N/A'}${exitExplanation && !isOOMKilled ? ` (${exitExplanation})` : ''}`} 
        />
        
        {term.signal && term.signal !== 0 && (
          <PropertyRow label="Signal" value={term.signal.toString()} />
        )}
        {term.reason && (
          <PropertyRow label="Reason" value={term.reason} />
        )}
        {term.message && (
          <PropertyRow label="Message" value={term.message} />
        )}
        
        {/* Container ID for debugging */}
        {term.containerID && (
          <PropertyRow 
            label="Container ID" 
            value={shortenContainerId(term.containerID)} 
            copyText={term.containerID}
            muted
            mono
          />
        )}
      </div>
    </div>
  );
};

const ContainerDropdown: React.FC<ContainerDropdownProps> = ({
  container,
  containerSpec,
  portForwards,
  loadingPorts,
  onPortForward,
  volumes,
}) => {
  const imageShort = container.image?.split('/').pop() || container.image;

  return (
    <div className="container-dropdown">
      <PropertyRow label="Image" value={imageShort} copyText={container.image} />

      {container.imageID && (
        <PropertyRow
          label="Image ID"
          value={container.imageID.replace('docker-pullable://', '').slice(0, 20) + '...'}
          copyText={container.imageID}
          muted
          mono
        />
      )}

      {containerSpec.ports?.length > 0 && (
        <CollapsibleRow title={`Ports (${containerSpec.ports.length})`}>
          <div className="container-ports">
            {containerSpec.ports.map((port: any, idx: number) => {
              const portKey = `${container.name}-${port.containerPort}`;
              const isForwarded = !!portForwards[portKey];
              const isLoading = loadingPorts.has(portKey);
              return (
                <div key={idx} className="port-row">
                  <span className="port-label">{port.name || 'port'}</span>
                  <span className="port-value">{port.containerPort}/{port.protocol || 'TCP'}</span>
                  <button
                    className={`port-btn ${isForwarded ? 'active' : ''}`}
                    onClick={(e) => { e.stopPropagation(); if (!isLoading) onPortForward(container.name, port.containerPort); }}
                    disabled={isLoading}
                  >
                    {isLoading ? '...' : isForwarded ? `${portForwards[portKey].localPort}` : 'FWD'}
                  </button>
                </div>
              );
            })}
          </div>
        </CollapsibleRow>
      )}

      {containerSpec.resources?.requests && (
        <CollapsibleRow title="Requests">
          <ResourceDisplay resources={containerSpec.resources.requests} />
        </CollapsibleRow>
      )}

      {containerSpec.resources?.limits && (
        <CollapsibleRow title="Limits">
          <ResourceDisplay resources={containerSpec.resources.limits} />
        </CollapsibleRow>
      )}

      {(() => {
        const envVars: Record<string, string> = {};
        containerSpec.envFrom?.forEach((ef: any) => {
          envVars['[Source]'] = ef.configMapRef ? `CM:${ef.configMapRef.name}` : ef.secretRef ? `Secret:${ef.secretRef.name}` : 'Unknown';
        });
        containerSpec.env?.forEach((env: any) => {
          let val = env.value || '-';
          if (!env.value && env.valueFrom) {
            if (env.valueFrom.fieldRef) val = `$(${env.valueFrom.fieldRef.fieldPath})`;
            else if (env.valueFrom.secretKeyRef) val = `${env.valueFrom.secretKeyRef.name}[${env.valueFrom.secretKeyRef.key}]`;
            else if (env.valueFrom.configMapKeyRef) val = `${env.valueFrom.configMapKeyRef.name}[${env.valueFrom.configMapKeyRef.key}]`;
            else if (env.valueFrom.resourceFieldRef) val = env.valueFrom.resourceFieldRef.resource;
          }
          envVars[env.name] = val;
        });
        if (Object.keys(envVars).length === 0) return null;
        const total = Object.keys(envVars).length;
        return (
          <CollapsibleRow title={`Env (${total})`}>
            <EnvList envVars={envVars} />
          </CollapsibleRow>
        );
      })()}

      {(() => {
        if (!containerSpec.volumeMounts?.length) return null;
        return (
          <CollapsibleRow title={`Mounts (${containerSpec.volumeMounts.length})`}>
            <div className="mounts-list">
              {containerSpec.volumeMounts.map((mount: any, i: number) => {
                const vol = volumes?.find((v) => v.name === mount.name);
                let type = '';
                if (vol?.secret) type = `Secret:${vol.secret.secretName}`;
                else if (vol?.configMap) type = `CM:${vol.configMap.name}`;
                else if (vol?.persistentVolumeClaim) type = `PVC:${vol.persistentVolumeClaim.claimName}`;
                else if (vol?.emptyDir) type = 'EmptyDir';
                else if (vol?.hostPath) type = `Host:${vol.hostPath.path}`;
                else if (vol?.projected) type = 'Projected';
                return (
                  <div key={i} className="mount-item">
                    <span className="mount-name">{mount.name}</span>
                    <span className="mount-path">{mount.mountPath}</span>
                    {mount.readOnly && <span className="mount-ro">RO</span>}
                    {type && <span className="mount-type">{type}</span>}
                  </div>
                );
              })}
            </div>
          </CollapsibleRow>
        );
      })()}

      {(containerSpec.command || containerSpec.args) && (() => {
        const cmd = [...(containerSpec.command || []), ...(containerSpec.args || [])];
        if (!cmd.length) return null;
        return (
          <CollapsibleRow title="Command">
            <pre className="command-block">{cmd.join(' ')}</pre>
          </CollapsibleRow>
        );
      })()}

      {/* Current termination status (if container is currently terminated) */}
      <CurrentTerminationStatus state={container.state} />
      
      {/* Last termination status (important for OOMKilled, crash loops, etc.) */}
      <LastTerminationStatus lastState={container.lastState} />
    </div>
  );
};

export default ContainerDropdown;
