import React, { useMemo } from 'react';

interface CRDDefinitionViewProps {
  crd: any;
}

const CRDDefinitionView: React.FC<CRDDefinitionViewProps> = ({ crd }) => {
  const spec = crd?.spec || {};
  const names = spec.names || {};
  const versions = (spec.versions || []) as any[];
  const storageVersion = useMemo(
    () => versions.find((v) => v.storage) || versions[0],
    [versions],
  );
  const schema = (storageVersion?.schema?.openAPIV3Schema || {}) as any;
  const requiredSet = new Set<string>(schema.required || []);

  const renderType = (s: any) => s?.type || (s?.properties ? 'object' : '');

  const renderProps = (props: any, requiredParent?: Set<string>, path?: string) => {
    if (!props) return null;
    const entries = Object.entries(props) as [string, any][];
    return (
      <div className="crd-props">
        {entries.map(([key, val]) => {
          const isReq = (requiredParent || new Set()).has(key);
          const t = renderType(val);
          const nextReq = new Set<string>(val.required || []);
          return (
            <details key={(path || '') + key} className="crd-prop" open={false}>
              <summary className="crd-prop-header">
                <span className="crd-prop-name">{key}</span>
                <div className="crd-prop-meta">
                  <span className="crd-prop-type">{t}</span>
                  {isReq && <span className="badge">required</span>}
                </div>
              </summary>
              <div className="crd-prop-body">
                {val.description && (
                  <div className="crd-prop-desc">{val.description}</div>
                )}
                {val.enum && Array.isArray(val.enum) && val.enum.length > 0 && (
                  <div className="crd-prop-enum">enum: {val.enum.join(', ')}</div>
                )}
                {val.properties && renderProps(val.properties, nextReq, (path || '') + key + '.')} 
                {val.items && (val.items.properties || val.items.type) && (
                  <div className="crd-prop-items">
                    <div className="crd-prop-items-title">items</div>
                    {val.items.properties
                      ? renderProps(val.items.properties, new Set(val.items.required || []), (path || '') + key + '[].')
                      : <div className="crd-prop-type">{val.items.type}</div>}
                  </div>
                )}
              </div>
            </details>
          );
        })}
      </div>
    );
  };

  return (
    <div className="info-section">
      <h3 className="info-section-title">Definition</h3>
      <div className="info-section-content">
        <div className="info-row"><div className="info-label"><span>Kind</span></div><div className="info-value"><span className="info-value-text">{names.kind}</span></div></div>
        <div className="info-row"><div className="info-label"><span>Group</span></div><div className="info-value"><span className="info-value-text">{spec.group}</span></div></div>
        <div className="info-row"><div className="info-label"><span>Scope</span></div><div className="info-value"><span className="info-value-text">{spec.scope}</span></div></div>
        {Array.isArray(names.shortNames) && names.shortNames.length > 0 && (
          <div className="info-row"><div className="info-label"><span>Short names</span></div><div className="info-value"><span className="info-value-text">{names.shortNames.join(', ')}</span></div></div>
        )}
        {versions.length > 0 && (
          <div className="info-row"><div className="info-label"><span>Versions</span></div><div className="info-value"><span className="info-value-text">{versions.map((v) => v.name + (v.storage ? ' (storage)' : '')).join(', ')}</span></div></div>
        )}
      </div>

      <h3 className="info-section-title" style={{ marginTop: 12 }}>Schema{storageVersion?.name ? ` (${storageVersion.name})` : ''}</h3>
      <div className="info-section-content">
        {schema?.properties ? (
          <div className="crd-schema">
            {schema.properties.spec && (
              <details className="crd-prop" open>
                <summary className="crd-prop-header">
                  <span className="crd-prop-name">spec</span>
                  <div className="crd-prop-meta">
                    <span className="crd-prop-type">object</span>
                    {requiredSet.has('spec') && <span className="badge">required</span>}
                  </div>
                </summary>
                <div className="crd-prop-body">
                  {renderProps(schema.properties.spec.properties, new Set(schema.properties.spec.required || []), 'spec.')}
                </div>
              </details>
            )}
            {schema.properties.status && (
              <details className="crd-prop">
                <summary className="crd-prop-header">
                  <span className="crd-prop-name">status</span>
                  <div className="crd-prop-meta">
                    <span className="crd-prop-type">object</span>
                  </div>
                </summary>
                <div className="crd-prop-body">
                  {renderProps(schema.properties.status.properties, new Set(schema.properties.status.required || []), 'status.')}
                </div>
              </details>
            )}
          </div>
        ) : (
          <div>No schema available</div>
        )}
      </div>
    </div>
  );
};

export default CRDDefinitionView;


