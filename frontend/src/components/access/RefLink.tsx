import React from 'react';
import ResourceLink from '../ResourceLink';
import type { ObjectRef, SubjectRef } from '../../types/rbac';
import { apiVersionOf, isLinkable } from './accessUtils';

interface Props {
  cluster: string;
  refObj: ObjectRef | SubjectRef;
  /** Show "ns/name" for namespaced objects. */
  qualified?: boolean;
  showKind?: boolean;
}

/** A Role, binding or service account mention that opens the inspector;
 * Users and Groups (not API objects) render as text. */
const RefLink: React.FC<Props> = ({ cluster, refObj, qualified = true, showKind = false }) => {
  const label = `${qualified && refObj.namespace ? `${refObj.namespace}/` : ''}${refObj.name}`;
  const kind = showKind ? <span className="access-ref-kind">{refObj.kind}</span> : null;
  if (!isLinkable(refObj)) {
    return (
      <span className="access-ref">
        {kind}
        <span className="access-ref-text">{label}</span>
      </span>
    );
  }
  return (
    <span className="access-ref">
      {kind}
      <ResourceLink
        cluster={cluster}
        apiVersion={apiVersionOf(refObj.kind)}
        kind={refObj.kind}
        name={refObj.name}
        namespace={refObj.namespace || undefined}
        openInDetailTab
      >
        {label}
      </ResourceLink>
    </span>
  );
};

export default RefLink;
