import React, { useState, useCallback } from 'react';
import { LockClosedIcon, MagnifyingGlassIcon, EyeOpenIcon, EyeClosedIcon, ChevronDownIcon, ChevronUpIcon } from '@radix-ui/react-icons';
import Editor from '@monaco-editor/react';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import ClipboardCopy from '../../common/ClipboardCopy';
import MetadataSection from '../shared/MetadataSection';
import EventsSection from '../shared/EventsSection';
import { formatBytes, decodeSecret } from '../../../utils/detailViewFormatters';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../../../utils/monacoTheme';
import { useStore } from '../../../store';
import { useShallow } from 'zustand/react/shallow';
import { DetailViewPropsWithCluster } from '../../../types/detailView';
import './SecretDetailView.css';

const SECRET_TYPES: Record<string, { label: string; desc: string }> = {
  'Opaque': { label: 'Opaque', desc: 'Arbitrary data' },
  'kubernetes.io/service-account-token': { label: 'SA Token', desc: 'Service account token' },
  'kubernetes.io/dockercfg': { label: 'Docker', desc: 'Docker config' },
  'kubernetes.io/dockerconfigjson': { label: 'Docker JSON', desc: 'Docker config.json' },
  'kubernetes.io/basic-auth': { label: 'Basic Auth', desc: 'Basic auth credentials' },
  'kubernetes.io/ssh-auth': { label: 'SSH Auth', desc: 'SSH credentials' },
  'kubernetes.io/tls': { label: 'TLS', desc: 'TLS certificate/key' },
  'bootstrap.kubernetes.io/token': { label: 'Bootstrap', desc: 'Bootstrap token' },
};

const detectLanguage = (key: string, value: string): string => {
  const ext = key.split('.').pop()?.toLowerCase();
  if (ext === 'json') return 'json';
  if (ext === 'yaml' || ext === 'yml') return 'yaml';
  if (ext === 'xml') return 'xml';
  if (ext === 'crt' || ext === 'pem' || ext === 'cert') return 'plaintext';
  if (ext === 'key') return 'plaintext';
  if (ext === 'js') return 'javascript';
  if (ext === 'ts') return 'typescript';
  if (ext === 'sh' || ext === 'bash') return 'shell';
  if (ext === 'py') return 'python';
  
  try {
    JSON.parse(value);
    return 'json';
  } catch {}
  
  if (value.match(/^[\w-]+:\s/m)) return 'yaml';
  if (value.includes('-----BEGIN')) return 'plaintext';
  
  return 'plaintext';
};

const SecretDetailView: React.FC<DetailViewPropsWithCluster> = ({ resource, cluster, handleResourceClick }) => {
  const { metadata = {}, data = {}, type = 'Opaque' } = resource;
  const { openBottomTab } = useStore(useShallow((s) => ({ openBottomTab: s.openBottomTab })));
  const [searchTerm, setSearchTerm] = useState('');
  const [revealedKeys, setRevealedKeys] = useState<Record<string, boolean>>({});
  const [expandedKeys, setExpandedKeys] = useState<Record<string, boolean>>({});

  const handleEdit = () => {
    openBottomTab('edit', resource, cluster);
  };

  const dataKeys = Object.keys(data);
  const totalKeys = dataKeys.length;
  const typeInfo = SECRET_TYPES[type] || { label: type.split('/').pop() || type, desc: 'Custom type' };

  const totalSize = Object.values(data).reduce((acc: number, val: any) => acc + decodeSecret(val).length, 0);

  const filteredKeys = dataKeys.filter(key => !searchTerm || key.toLowerCase().includes(searchTerm.toLowerCase()));

  const allRevealed = dataKeys.length > 0 && dataKeys.every(key => revealedKeys[key]);
  const anyExpanded = dataKeys.some(key => expandedKeys[key]);

  const toggleShowAll = () => {
    const newState = dataKeys.reduce((acc, key) => ({ ...acc, [key]: !allRevealed }), {});
    setRevealedKeys(newState);
  };

  const toggleExpandAll = () => {
    const newState = anyExpanded ? {} : dataKeys.reduce((acc, key) => ({ ...acc, [key]: true }), {});
    setExpandedKeys(newState);
  };

  const toggleKeyExpand = (key: string) => {
    setExpandedKeys(prev => ({ ...prev, [key]: !prev[key] }));
  };

  const handleEditorWillMount = useCallback((monaco: any) => {
    installKanivetMonacoTheme(monaco);
  }, []);

  return (
    <>
      <PropertyGroup title="Secret Info" icon={<LockClosedIcon />} defaultOpen>
        <PropertyRow
          label="Type"
          value={<><span className="secret-type-badge">{typeInfo.label}</span><span className="secret-type-desc">{typeInfo.desc}</span></>}
        />
        <PropertyRow label="Keys" value={totalKeys} />
        <PropertyRow label="Total Size" value={formatBytes(totalSize)} />
      </PropertyGroup>
      <div className="section-divider" />

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} />
      <div className="section-divider" />

      {totalKeys > 0 && (
        <>
          <PropertyGroup
            title="Data"
            count={totalKeys}
            defaultOpen
            actions={
              <div className="secret-controls">
                <button className="secret-control-btn" onClick={toggleShowAll} title={allRevealed ? 'Hide All' : 'Reveal All'}>
                  {allRevealed ? <EyeClosedIcon /> : <EyeOpenIcon />}
                </button>
                <button className="secret-control-btn" onClick={toggleExpandAll} title={anyExpanded ? 'Collapse All' : 'Expand All'}>
                  {anyExpanded ? <ChevronUpIcon /> : <ChevronDownIcon />}
                </button>
              </div>
            }
          >
            <div className="secret-search">
              <MagnifyingGlassIcon />
              <input
                type="text"
                placeholder="Filter secrets..."
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
              />
            </div>
            <div className="secret-list">
              {filteredKeys.length === 0 && searchTerm ? (
                <div className="no-results">No matching secrets</div>
              ) : (
                filteredKeys.map(key => {
                  const encodedValue = data[key];
                  const decodedValue = decodeSecret(encodedValue);
                  const isRevealed = revealedKeys[key] || false;
                  const isExpanded = expandedKeys[key] || false;
                  const lines = decodedValue.split('\n');
                  const lineCount = lines.length;
                  const isMultiline = lineCount > 5;
                  const language = detectLanguage(key, decodedValue);
                  const displayHeight = isExpanded 
                    ? Math.min(lineCount * 19 + 20, 600) 
                    : (isMultiline ? 150 : Math.min(lineCount * 19 + 20, 150));

                  return (
                    <div key={key} className="secret-entry">
                      <div className="secret-entry-header">
                        <div className="secret-entry-key">
                          <LockClosedIcon className="lock-icon" />
                          <span className="key-name">{key}</span>
                          <span className="format-badge format-secondary">{language.toUpperCase()}</span>
                        </div>
                        <div className="secret-entry-meta">
                          <span className="entry-size">{formatBytes(new Blob([decodedValue]).size)}</span>
                          <span className="entry-lines">{lineCount} lines</span>
                          {isMultiline && isRevealed && (
                            <button
                              className="expand-toggle-btn"
                              onClick={() => toggleKeyExpand(key)}
                              title={isExpanded ? 'Collapse' : 'Expand'}
                            >
                              {isExpanded ? 'Collapse' : 'Expand'}
                            </button>
                          )}
                          <button
                            className="reveal-btn"
                            onClick={() => setRevealedKeys(prev => ({ ...prev, [key]: !isRevealed }))}
                            title={isRevealed ? 'Hide' : 'Reveal'}
                          >
                            {isRevealed ? <EyeClosedIcon /> : <EyeOpenIcon />}
                          </button>
                          {isRevealed && <ClipboardCopy text={decodedValue} />}
                        </div>
                      </div>
                      {isRevealed && (
                        <div 
                          className="secret-entry-value monaco-wrapper clickable-editor"
                          onClick={handleEdit}
                          title="Click to edit in bottom pane"
                        >
                          <Editor
                            height={displayHeight}
                            language={language}
                            theme={KANIVET_MONACO_THEME}
                            value={decodedValue}
                            beforeMount={handleEditorWillMount}
                            options={{
                              readOnly: true,
                              minimap: { enabled: false },
                              fontSize: 12,
                              lineNumbers: 'on',
                              wordWrap: 'on',
                              scrollBeyondLastLine: false,
                              automaticLayout: true,
                              fontFamily: "'JetBrains Mono', 'SF Mono', 'Cascadia Code', 'Fira Code', 'Monaco', 'Menlo', monospace",
                              renderWhitespace: 'selection',
                              scrollbar: {
                                vertical: 'visible',
                                horizontal: 'visible',
                                verticalScrollbarSize: 8,
                                horizontalScrollbarSize: 8,
                              },
                              folding: true,
                              lineDecorationsWidth: 0,
                              lineNumbersMinChars: 3,
                              glyphMargin: false,
                              contextmenu: false,
                            }}
                          />
                        </div>
                      )}
                    </div>
                  );
                })
              )}
            </div>
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <EventsSection events={resource.events} />
    </>
  );
};

export default SecretDetailView;
