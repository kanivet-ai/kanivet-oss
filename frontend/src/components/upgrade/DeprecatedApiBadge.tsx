import React, { useEffect, useState } from 'react';
import { getUpgradeCatalog } from '../../services/api/upgrade';
import type { UpgradeCatalog } from '../../types/upgrade';
import { badgeTone, objectDeprecations } from './upgradeView';
import { openUpgradeReadiness } from './openUpgradeReadiness';
import './DeprecatedApiBadge.css';

let cached: UpgradeCatalog | null = null;

/**
 * A small inspector badge for an object that uses a deprecated API, in its
 * own apiVersion or in the manifest recorded by last-applied-configuration.
 * Clicking it opens the cluster's Upgrade readiness page.
 */
const DeprecatedApiBadge: React.FC<{ resource: any; cluster: string }> = ({ resource, cluster }) => {
  const [catalog, setCatalog] = useState<UpgradeCatalog | null>(cached);

  useEffect(() => {
    if (cached) return;
    let alive = true;
    getUpgradeCatalog()
      .then((c) => {
        cached = c;
        if (alive) setCatalog(c);
      })
      .catch(() => { /* badge is optional */ });
    return () => { alive = false; };
  }, []);

  const found = objectDeprecations(catalog, resource);
  if (!found.length) return null;
  const first = found[0];
  const d = first.deprecation;
  const tone = badgeTone(d);
  const where = first.via === 'last-applied' ? 'Its last-applied manifest uses' : 'Served as';
  const title = `${where} ${first.apiVersion}, ${d.removedIn ? `removed in Kubernetes ${d.removedIn}` : `deprecated since ${d.deprecatedIn}`}. ${d.replacement ? `Use ${d.replacement}.` : 'No replacement API.'} Click to open Upgrade readiness.`;

  return (
    <button
      type="button"
      className={`ap-badge ap-badge--sm ap-badge--${tone} deprecated-api-badge`}
      title={title}
      aria-label={title}
      onClick={() => void openUpgradeReadiness(cluster)}
    >
      Deprecated API
    </button>
  );
};

export default DeprecatedApiBadge;
