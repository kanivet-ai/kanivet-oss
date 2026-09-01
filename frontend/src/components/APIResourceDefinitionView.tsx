import React from 'react';

const Row = ({ label, children }: { label: string; children?: React.ReactNode }) => (
  <div className="info-row">
    <div className="info-label"><span>{label}</span></div>
    <div className="info-value"><span className="info-value-text">{children}</span></div>
  </div>
);

const APIResourceDefinitionView = ({ resource }: { resource: any }) => {
  const r = resource?.apiResource || {};
  const group = r.group || resource.group || '';
  const version = r.version || resource.version || '';
  const kind = r.kind || resource.kindName || resource.kind || '';
  const name = r.name || resource.resourceName || '';
  const namespaced = r.namespaced === true;
  const verbs = Array.isArray(r.verbs) ? r.verbs.join(', ') : '';
  const shortNames = Array.isArray(r.shortNames) ? r.shortNames.join(', ') : '';
  const categories = Array.isArray(r.categories) ? r.categories.join(', ') : '';
  const singularName = r.singularName || '';
  const displayName = `${kind}`;
  return (
    <div className="info-section">
      <h3 className="info-section-title">Definition</h3>
      <div className="info-section-content">
        <Row label="Kind">{displayName}</Row>
        <Row label="Group">{group || '(core)'}</Row>
        <Row label="Version">{version}</Row>
        <Row label="Resource">{name}</Row>
        {singularName && <Row label="Singular">{singularName}</Row>}
        <Row label="Namespaced">{namespaced ? 'true' : 'false'}</Row>
        {verbs && <Row label="Verbs">{verbs}</Row>}
        {shortNames && <Row label="Short names">{shortNames}</Row>}
        {categories && <Row label="Categories">{categories}</Row>}
      </div>
    </div>
  );
};

export default APIResourceDefinitionView;


