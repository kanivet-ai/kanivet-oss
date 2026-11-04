import React, { useCallback, useEffect, useState } from 'react';
import { HeartIcon } from '@radix-ui/react-icons';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import { addHealthIgnore, getResourceHealth } from '../../services/api/health';
import type { HealthFinding, HealthRef, ResourceHealth } from '../../types/health';
import { useIssues } from './healthStore';
import { ignoreFor } from './healthView';
import { IssueRow } from './HealthParts';
import { FindingRow } from './FindingRow';
import './Health.css';

/** Kinds that have issues rules or audit checks. */
export const HEALTH_KINDS = new Set([
  'Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'Service', 'Ingress',
  'PersistentVolumeClaim', 'Node', 'HorizontalPodAutoscaler', 'ReplicaSet',
]);

/**
 * The inspector's Health section: the object's live issues and its audit
 * findings with fixes. It hides itself when there is nothing to show.
 */
const ResourceHealthSection: React.FC<{ cluster: string; resource: HealthRef }> = ({ cluster, resource }) => {
  const { updatedAt } = useIssues(cluster);
  const [data, setData] = useState<ResourceHealth | null>(null);
  const [error, setError] = useState(false);
  const { kind, namespace, name, apiVersion } = resource;

  const load = useCallback(() => {
    const ctrl = new AbortController();
    getResourceHealth(cluster, { kind, namespace, name, apiVersion }, ctrl.signal)
      .then((d) => { setData(d); setError(false); })
      .catch((e) => { if (e?.name !== 'CanceledError') setError(true); });
    return () => ctrl.abort();
  }, [cluster, kind, namespace, name, apiVersion]);

  useEffect(load, [load, updatedAt]);

  if (error || !data) return null;
  const active = data.findings.filter((f) => !f.ignored);
  if (data.issues.length === 0 && active.length === 0) return null;
  const checks = new Map(data.checks.map((c) => [c.id, c]));
  const onIgnore = async (f: HealthFinding, scope: 'resource' | 'namespace') => {
    await addHealthIgnore(ignoreFor(cluster, f, scope));
    load();
  };
  const findingOwner = active[0]?.resource;

  return (
    <>
      <PropertyGroup title="Health" icon={<HeartIcon />} count={data.issues.length + active.length} defaultOpen persistKey="health">
        <div className="health-inspector">
          {data.issues.length > 0 && (
            <div className="ap-card health-card">
              {data.issues.map((i) => <IssueRow key={i.id} cluster={cluster} issue={i} compact />)}
            </div>
          )}
          {active.length > 0 && (
            <>
              <div className="health-inspector-label">
                Best practice{findingOwner && findingOwner.kind !== kind ? ` (from ${findingOwner.kind} ${findingOwner.name})` : ''}
              </div>
              <div className="ap-card health-card">
                {active.map((f, i) => (
                  <FindingRow key={`${f.checkId}-${f.container}-${i}`} cluster={cluster} f={f} check={checks.get(f.checkId)} onIgnore={onIgnore} />
                ))}
              </div>
            </>
          )}
        </div>
      </PropertyGroup>
      <div className="section-divider" />
    </>
  );
};

export default ResourceHealthSection;
