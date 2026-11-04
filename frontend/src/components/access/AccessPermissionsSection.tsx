import React, { useEffect, useState } from 'react';
import { LockClosedIcon } from '@radix-ui/react-icons';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import * as rbacApi from '../../services/api/rbac';
import type { PermissionMatrix, SubjectRef } from '../../types/rbac';
import { getErrorMessage } from '../../utils/errorMessage';
import { openAccessPage } from './accessIntent';
import PermissionMatrixView from './PermissionMatrixView';
import RefLink from './RefLink';
import './Access.css';

export const ACCESS_SECTION_KINDS = ['ServiceAccount', 'Role', 'ClusterRole', 'RoleBinding', 'ClusterRoleBinding', 'Pod'];

interface Props {
  resource: any;
  cluster: string;
}

/** The inspector's "Permissions" section for RBAC objects and pods. */
const AccessPermissionsSection: React.FC<Props> = ({ resource, cluster }) => {
  const kind: string = resource?.kind;
  const name: string = resource?.metadata?.name;
  const namespace: string | undefined = resource?.metadata?.namespace;
  const [matrix, setMatrix] = useState<PermissionMatrix | null>(null);
  const [subjects, setSubjects] = useState<SubjectRef[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);

  const podSA: SubjectRef | null =
    kind === 'Pod' && namespace
      ? { kind: 'ServiceAccount', namespace, name: resource?.spec?.serviceAccountName || resource?.spec?.serviceAccount || 'default' }
      : null;
  const subject: SubjectRef | null = kind === 'ServiceAccount' && namespace ? { kind, name, namespace } : podSA;

  useEffect(() => {
    setMatrix(null);
    setSubjects(null);
    setError(null);
  }, [cluster, kind, name, namespace]);

  useEffect(() => {
    if (!open || !cluster || !name || matrix || error) return;
    let alive = true;
    setLoading(true);
    const p: Promise<void> = subject
      ? rbacApi.getSubjectPermissions(cluster, subject, namespace).then((m) => { if (alive) setMatrix(m); })
      : kind === 'Role' || kind === 'ClusterRole'
        ? rbacApi.getRolePermissions(cluster, kind, name, namespace).then((m) => { if (alive) setMatrix(m); })
        : rbacApi.getBindingPermissions(cluster, kind, name, namespace).then((r) => {
          if (!alive) return;
          setMatrix(r.matrix);
          setSubjects(r.subjects || []);
        });
    p.catch((e) => alive && setError(getErrorMessage(e, 'Could not load permissions')))
      .finally(() => alive && setLoading(false));
    return () => { alive = false; };
  }, [open, cluster, kind, name, namespace, matrix, error]);

  if (!ACCESS_SECTION_KINDS.includes(kind) || !name) return null;

  const openInPage = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (subject) void openAccessPage(cluster, { mode: 'subject', subject, namespace: namespace || '' });
    else void openAccessPage(cluster, { mode: 'risks' });
  };

  const actions = subject ? (
    <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={openInPage}>
      Open in Access
    </button>
  ) : undefined;

  const riskCount = matrix?.risks?.length || 0;

  return (
    <>
      <PropertyGroup
        title={kind === "Role" || kind === "ClusterRole" ? "Permission matrix" : "Permissions"}
        icon={<LockClosedIcon />}
        persistKey={`access-permissions-${kind === 'Pod' ? 'pod' : 'rbac'}`}
        defaultOpen={kind !== 'Pod'}
        onOpenChange={setOpen}
        actions={actions}
      >
        <OpenProbe onOpen={() => setOpen(true)} />
        <div className="access-inspector">
          {podSA && (
            <div className="access-inspector-note">
              Runs as service account <RefLink cluster={cluster} refObj={podSA} qualified={false} />
              {riskCount > 0 && <span className="ap-badge ap-badge--sm ap-badge--warning">{riskCount} risky</span>}
            </div>
          )}
          {subjects && (
            <div className="access-inspector-subjects">
              <span className="access-query-word">Subjects</span>
              {subjects.length === 0 && <span className="access-ref-text">none</span>}
              {subjects.map((s, i) => (
                <span key={i} className="access-chip access-chip--static">
                  <span className="access-chip-kind">{s.kind === 'ServiceAccount' ? 'SA' : s.kind}</span>
                  <RefLink cluster={cluster} refObj={s} />
                </span>
              ))}
            </div>
          )}
          {loading && !matrix && <div className="access-empty"><span className="ap-spinner" /> Loading…</div>}
          {error && <div className="access-empty">{error}</div>}
          {matrix && (
            <PermissionMatrixView
              cluster={cluster}
              matrix={matrix}
              compact
              showBindings
              emptyText={subject ? 'No binding grants this service account anything.' : 'This role has no rules.'}
            />
          )}
        </div>
      </PropertyGroup>
      <div className="section-divider" />
    </>
  );
};

// PropertyGroup reports open changes only on toggle; this mounts with the
// content, so a section that starts open (or was persisted open) loads too.
const OpenProbe: React.FC<{ onOpen: () => void }> = ({ onOpen }) => {
  useEffect(() => { onOpen(); }, []);
  return null;
};

export default AccessPermissionsSection;
