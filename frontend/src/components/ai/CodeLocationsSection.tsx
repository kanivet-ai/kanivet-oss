import React, { useEffect, useState } from 'react';
import { PlusIcon, TrashIcon } from '@radix-ui/react-icons';
import { aiChatApi, type CodeLocation } from '../../services/api/aiChat';
import { pickFolder } from './FixChooser';

/** AI & MCP settings: the local folders Kanivet maps resources to for
 * "Fix in code". Stored in ~/.kanivet/ai/code-locations.json. */
const CodeLocationsSection: React.FC = () => {
  const [locs, setLocs] = useState<CodeLocation[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    aiChatApi.locations().then(setLocs).catch(() => setLocs([]));
  }, []);
  const add = async () => {
    const p = await pickFolder();
    if (!p) return;
    try {
      setErr(null);
      setLocs(await aiChatApi.addLocation(p));
    } catch (e: any) {
      setErr(e?.response?.data?.error || e?.message || 'Could not add the folder');
    }
  };
  return (
    <>
      <div className="ap-group-label ai-label-row">
        <span>Code locations</span>
        <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={add}><PlusIcon /> Add folder…</button>
      </div>
      <div className="ap-card ai-section">
        {locs === null ? (
          <div className="ap-card-row ai-muted ai-small">Loading…</div>
        ) : locs.length === 0 ? (
          <div className="ap-card-row ai-muted ai-small">No folders yet. Add your GitOps, Helm chart or Terraform checkouts; Kanivet matches them to resources by git remote (Argo CD/Flux source URLs) and Helm chart name.</div>
        ) : (
          locs.map((l) => (
            <div key={l.path} className="ap-card-row">
              <span className="ai-row-label">
                {l.name}
                <span className="ai-muted ai-small ap-truncate" title={l.path}>{l.remotes?.[0] || l.path}</span>
                {l.charts && l.charts.length > 0 && <span className="ap-badge ap-badge--sm">charts: {l.charts.slice(0, 3).join(', ')}{l.charts.length > 3 ? '…' : ''}</span>}
              </span>
              <button type="button" className="ap-icon-btn" aria-label={`Remove ${l.name}`} title="Remove" onClick={async () => setLocs(await aiChatApi.removeLocation(l.path))}><TrashIcon /></button>
            </div>
          ))
        )}
        {err && <div className="ap-card-row ai-small" role="alert">{err}</div>}
        <div className="ap-card-row ai-muted ai-small">
          Fix in code runs the agent in the chosen folder with file read/edit tools only (no shell), on a new kanivet/fix-… branch by default. Nothing is committed or pushed unless you click Commit.
        </div>
      </div>
    </>
  );
};

export default CodeLocationsSection;
