import React, { useState, useCallback } from 'react';
import { MagnifyingGlassIcon, FileTextIcon, ArchiveIcon } from '@radix-ui/react-icons';
import Editor from '@monaco-editor/react';
import PropertyRow from '../../common/PropertyRow';
import PropertyGroup from '../shared/PropertyGroup';
import MetadataSection from '../shared/MetadataSection';
import EventsSection from '../shared/EventsSection';
import ClipboardCopy from '../../common/ClipboardCopy';
import { formatBytes } from '../../../utils/detailViewFormatters';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../../../utils/monacoTheme';
import { useStore } from '../../../store';
import { DetailViewPropsWithCluster } from '../../../types/detailView';
import './ConfigMapDetailView.css';

const detectLanguage = (key: string, value: string): string => {
  const ext = key.split('.').pop()?.toLowerCase();
  if (ext === 'json') return 'json';
  if (ext === 'yaml' || ext === 'yml') return 'yaml';
  if (ext === 'xml') return 'xml';
  if (ext === 'html') return 'html';
  if (ext === 'js') return 'javascript';
  if (ext === 'ts') return 'typescript';
  if (ext === 'sh' || ext === 'bash') return 'shell';
  if (ext === 'py') return 'python';
  if (ext === 'sql') return 'sql';
  if (ext === 'ini' || ext === 'conf' || ext === 'config') return 'ini';
  if (ext === 'properties') return 'properties';
  
  try {
    JSON.parse(value);
    return 'json';
  } catch {}
  
  if (value.match(/^[\w-]+:\s/m)) return 'yaml';
  
  return 'plaintext';
};

const ConfigMapDetailView: React.FC<DetailViewPropsWithCluster> = ({ resource, cluster, handleResourceClick }) => {
  const { metadata = {}, data = {}, binaryData = {} } = resource;
  const { openBottomTab } = useStore();
  const [searchTerm, setSearchTerm] = useState('');
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(new Set());

  const handleEdit = () => {
    openBottomTab('edit', resource, cluster);
  };

  const dataKeys = Object.keys(data);
  const binaryKeys = Object.keys(binaryData);
  const totalKeys = dataKeys.length + binaryKeys.length;

  const processedBinaryData: Record<string, string> = {};
  Object.keys(binaryData).forEach(key => {
    try { processedBinaryData[key] = atob(binaryData[key]); } catch { processedBinaryData[key] = '<Invalid Base64>'; }
  });

  const totalSize = Object.values(data).reduce((acc: number, val: any) => acc + new Blob([val]).size, 0) +
    Object.values(binaryData).reduce((acc: number, val: any) => { try { return acc + atob(val).length; } catch { return acc; } }, 0);

  const filterEntry = ([key, value]: [string, unknown]) =>
    !searchTerm || key.toLowerCase().includes(searchTerm.toLowerCase()) || String(value).toLowerCase().includes(searchTerm.toLowerCase());

  const filteredData = Object.entries(data as Record<string, string>).filter(filterEntry);
  const filteredBinaryData = Object.entries(processedBinaryData).filter(filterEntry);

  const allData: Record<string, { value: string; isBinary: boolean }> = {};
  filteredData.forEach(([key, value]) => { allData[key] = { value, isBinary: false }; });
  filteredBinaryData.forEach(([key, value]) => { allData[key] = { value, isBinary: true }; });

  const toggleExpand = (key: string) => {
    setExpandedKeys(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const handleEditorWillMount = useCallback((monaco: any) => {
    installKanivetMonacoTheme(monaco);
  }, []);

  return (
    <>
      <PropertyGroup title="Summary" icon={<FileTextIcon />} defaultOpen>
        <PropertyRow label="Keys" value={totalKeys} />
        {dataKeys.length > 0 && <PropertyRow label="Data Keys" value={dataKeys.length} />}
        {binaryKeys.length > 0 && <PropertyRow label="Binary Keys" value={binaryKeys.length} />}
        <PropertyRow label="Total Size" value={formatBytes(totalSize)} />
        {resource.immutable && <PropertyRow label="Immutable" value={<span className="status-badge status-warning">Yes</span>} />}
      </PropertyGroup>
      <div className="section-divider" />

      <MetadataSection metadata={metadata} handleResourceClick={handleResourceClick} />
      <div className="section-divider" />

      {totalKeys > 0 && (
        <>
          <PropertyGroup title="Data" count={totalKeys} defaultOpen>
            <div className="configmap-search">
              <MagnifyingGlassIcon />
              <input
                type="text"
                placeholder="Filter by key or value..."
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
              />
            </div>
            <div className="configmap-entries">
              {Object.keys(allData).length === 0 && searchTerm ? (
                <div className="no-results">No matching keys found</div>
              ) : (
                Object.entries(allData).map(([key, { value, isBinary }]) => {
                  const lines = value.split('\n');
                  const lineCount = lines.length;
                  const isMultiline = lineCount > 5;
                  const isExpanded = expandedKeys.has(key);
                  const language = detectLanguage(key, value);
                  const displayHeight = isExpanded 
                    ? Math.min(lineCount * 19 + 20, 600) 
                    : (isMultiline ? 150 : Math.min(lineCount * 19 + 20, 150));

                  return (
                    <div key={key} className="configmap-entry">
                      <div className="configmap-entry-header">
                        <div className="configmap-entry-key">
                          {isBinary && <ArchiveIcon className="binary-icon" />}
                          <span className="key-name">{key}</span>
                          <span className="format-badge format-secondary">{language.toUpperCase()}</span>
                        </div>
                        <div className="configmap-entry-meta">
                          <span className="entry-size">{formatBytes(new Blob([value]).size)}</span>
                          <span className="entry-lines">{lineCount} lines</span>
                          {isMultiline && (
                            <button
                              className="expand-toggle-btn"
                              onClick={() => toggleExpand(key)}
                              title={isExpanded ? 'Collapse' : 'Expand'}
                            >
                              {isExpanded ? 'Collapse' : 'Expand'}
                            </button>
                          )}
                          <ClipboardCopy text={value} />
                        </div>
                      </div>
                      <div 
                        className="configmap-entry-value monaco-wrapper clickable-editor"
                        onClick={handleEdit}
                        title="Click to edit in bottom pane"
                      >
                        <Editor
                          height={displayHeight}
                          language={language}
                          theme={KANIVET_MONACO_THEME}
                          value={value}
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

export default ConfigMapDetailView;
