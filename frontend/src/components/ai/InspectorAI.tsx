import React from 'react';
import AIButton from './AIButton';
import { askAI, resourceAskSeed, type AIRef } from './aiContext';
import { SparkleIcon } from './AIIcons';

const WORKLOADS = new Set(['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Pod', 'Rollout']);

function refOf(resource: any): AIRef | null {
  const name = resource?.metadata?.name;
  if (!name || !resource?.kind) return null;
  return { kind: resource.kind, apiVersion: resource.apiVersion, namespace: resource.metadata?.namespace, name };
}

/** Inspector header: "Ask AI about this" (opens the chat with the resource
 * and its redacted YAML attached; the user types the question). */
export const InspectorAskAI: React.FC<{ resource: any; cluster: string }> = ({ resource, cluster }) => {
  const r = refOf(resource);
  if (!r) return null;
  return <AIButton variant="action" label="Ask AI" title="Ask AI about this resource (⌘J)" seed={() => resourceAskSeed(cluster, r, 'ask')} />;
};

/** Items for the inspector's "More" menu. */
export const InspectorAIMenuItems: React.FC<{ resource: any; cluster: string }> = ({ resource, cluster }) => {
  const r = refOf(resource);
  if (!r) return null;
  const item = (label: string, what: 'explain-yaml' | 'optimize' | 'fix') => (
    <button type="button" className="quick-action-btn tab-content-action-btn ai-menu-item" role="menuitem" onClick={() => askAI(resourceAskSeed(cluster, r, what))}>
      <span className="ap-action-icon ai-btn-icon"><SparkleIcon size={13} /></span>
      <span className="ap-action-label">{label}</span>
    </button>
  );
  return (
    <>
      {item('Explain this YAML', 'explain-yaml')}
      {WORKLOADS.has(r.kind) && item('Optimize resources', 'optimize')}
      {item('Fix with AI', 'fix')}
    </>
  );
};
