import { useEffect, useMemo, useState } from 'react';
import Dialog from '../common/Dialog';
import { ArgoSyncOptions } from '../../services/api/resources';
import './ArgoSyncDialog.css';

interface ResourceRef {
  group: string;
  kind: string;
  namespace?: string;
  name: string;
  syncStatus?: string;
}

interface Props {
  isOpen: boolean;
  appName: string;
  resources: ResourceRef[];
  isRunning: boolean;
  defaultRevision?: string;
  onCancel: () => void;
  onConfirm: (opts: ArgoSyncOptions) => void;
}

const ArgoSyncDialog = ({
  isOpen,
  appName,
  resources,
  isRunning,
  defaultRevision,
  onCancel,
  onConfirm,
}: Props) => {
  const [prune, setPrune] = useState(false);
  const [dryRun, setDryRun] = useState(false);
  const [force, setForce] = useState(false);
  const [replace, setReplace] = useState(false);
  const [strategy, setStrategy] = useState<'apply' | 'hook'>('apply');
  const [revision, setRevision] = useState(defaultRevision || '');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [scope, setScope] = useState<'all' | 'selected' | 'outofsync'>('all');

  useEffect(() => {
    if (isOpen) {
      setPrune(false);
      setDryRun(false);
      setForce(false);
      setReplace(false);
      setStrategy('apply');
      setRevision(defaultRevision || '');
      setSelected(new Set());
      setScope('all');
    }
  }, [isOpen, defaultRevision]);

  const refKey = (r: ResourceRef) => `${r.group || '_'}/${r.kind}/${r.namespace || '_'}/${r.name}`;

  const scopedResources = useMemo(() => {
    if (scope === 'outofsync') {
      return resources.filter((r) => r.syncStatus && r.syncStatus.toLowerCase() === 'outofsync');
    }
    if (scope === 'selected') {
      return resources.filter((r) => selected.has(refKey(r)));
    }
    return [];
  }, [scope, resources, selected]);

  const toggleSelect = (r: ResourceRef) => {
    setSelected((prev) => {
      const next = new Set(prev);
      const k = refKey(r);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  };

  const handleConfirm = () => {
    const opts: ArgoSyncOptions = {
      prune,
      dryRun,
      force,
      replace,
      strategy,
    };
    if (revision.trim()) opts.revision = revision.trim();
    if (scope !== 'all') {
      opts.resources = scopedResources.map((r) => ({
        group: r.group,
        kind: r.kind,
        namespace: r.namespace,
        name: r.name,
      }));
    }
    onConfirm(opts);
  };

  return (
    <Dialog
      isOpen={isOpen}
      title={`Sync ${appName}`}
      onClose={onCancel}
      onConfirm={handleConfirm}
      confirmText={isRunning ? 'Syncing…' : dryRun ? 'Run Dry-Run' : 'Sync'}
      cancelText="Cancel"
      isLoading={isRunning}
      confirmDisabled={scope === 'selected' && selected.size === 0}
    >
      <div className="argo-sync-dialog">
        <div className="argo-sync-group">
          <div className="argo-sync-group-title">Strategy</div>
          <div className="argo-sync-radio-row">
            <label>
              <input
                type="radio"
                checked={strategy === 'apply'}
                onChange={() => setStrategy('apply')}
              />
              Apply
            </label>
            <label>
              <input
                type="radio"
                checked={strategy === 'hook'}
                onChange={() => setStrategy('hook')}
              />
              Hook
            </label>
          </div>
        </div>

        <div className="argo-sync-group">
          <div className="argo-sync-group-title">Options</div>
          <label className="argo-sync-check">
            <input type="checkbox" checked={prune} onChange={(e) => setPrune(e.target.checked)} />
            Prune (delete resources no longer in source)
          </label>
          <label className="argo-sync-check">
            <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} />
            Dry-run (preview only)
          </label>
          <label className="argo-sync-check">
            <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
            Force (overwrite resources with target state)
          </label>
          <label className="argo-sync-check">
            <input type="checkbox" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
            Replace (use kubectl replace instead of apply)
          </label>
        </div>

        <div className="argo-sync-group">
          <div className="argo-sync-group-title">Revision</div>
          <input
            className="argo-sync-input"
            type="text"
            placeholder="Leave blank to use targetRevision"
            value={revision}
            onChange={(e) => setRevision(e.target.value)}
          />
        </div>

        <div className="argo-sync-group">
          <div className="argo-sync-group-title">Scope</div>
          <div className="argo-sync-radio-row">
            <label>
              <input type="radio" checked={scope === 'all'} onChange={() => setScope('all')} />
              All resources
            </label>
            <label>
              <input
                type="radio"
                checked={scope === 'outofsync'}
                onChange={() => setScope('outofsync')}
              />
              OutOfSync only
            </label>
            <label>
              <input
                type="radio"
                checked={scope === 'selected'}
                onChange={() => setScope('selected')}
              />
              Selected
            </label>
          </div>
          {scope === 'selected' && (
            <div className="argo-sync-resource-list">
              {resources.map((r) => {
                const k = refKey(r);
                return (
                  <label key={k} className="argo-sync-resource-row">
                    <input
                      type="checkbox"
                      checked={selected.has(k)}
                      onChange={() => toggleSelect(r)}
                    />
                    <span className="argo-sync-resource-kind">{r.kind}</span>
                    <span className="argo-sync-resource-name">
                      {r.namespace ? `${r.namespace}/${r.name}` : r.name}
                    </span>
                    {r.syncStatus && (
                      <span className={`argo-sync-resource-status ${r.syncStatus.toLowerCase()}`}>
                        {r.syncStatus}
                      </span>
                    )}
                  </label>
                );
              })}
            </div>
          )}
          {scope === 'outofsync' && (
            <div className="argo-sync-info">
              {scopedResources.length} OutOfSync resource{scopedResources.length === 1 ? '' : 's'}.
            </div>
          )}
        </div>
      </div>
    </Dialog>
  );
};

export default ArgoSyncDialog;
