import React, {
  useState,
  useEffect,
  useRef,
  useLayoutEffect,
  useCallback,
} from 'react';
import Editor from '@monaco-editor/react';
import * as yaml from 'js-yaml';
import { Cross2Icon, ExclamationTriangleIcon, CheckCircledIcon, UpdateIcon, CheckIcon, PlayIcon, CrossCircledIcon } from '@radix-ui/react-icons';
import api from '../services/api';
import { useStore } from '../store';
import { installKanivetMonacoTheme, KANIVET_MONACO_THEME } from '../utils/monacoTheme';
import ResourceSelector from './ResourceSelector';
import './YamlEditor.css';

const isMac = navigator.platform.startsWith('Mac');
const Kbd = ({ k }: { k: string }) => <kbd>{(isMac ? '⌘' : 'Ctrl+') + k}</kbd>;

interface YamlEditorProps {
  resource: any;
  cluster: string;
  mode?: 'edit' | 'create';
  onSave?: (updatedYaml: string) => void;
}

const YamlEditor = ({ resource, cluster, mode = 'edit', onSave }: YamlEditorProps) => {
  const { updateDetailData } = useStore();
  const [yamlContent, setYamlContent] = useState('');
  const [cursor, setCursor] = useState({ line: 1, col: 1 });
  const [yamlError, setYamlError] = useState<{ message: string; line?: number } | null>(null);
  const [isDirty, setIsDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [isDryRunning, setIsDryRunning] = useState(false);
  const monacoRef = useRef<any>(null);
  const editorRef = useRef<any>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const resizeObserverRef = useRef<ResizeObserver | null>(null);
  const successTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  
  const isHelmValues = resource && resource._isHelmValues;

  const handleEditorWillMount = (monaco: any) => {
    monacoRef.current = monaco;
    installKanivetMonacoTheme(monaco);
  };

  const handleEditorMount = useCallback(
    (editor: any) => {
      editorRef.current = editor;
      editor.onDidChangeCursorPosition((e: any) => setCursor({ line: e.position.lineNumber, col: e.position.column }));

      // Force initial layout
      requestAnimationFrame(() => {
        if (editorRef.current) {
          editorRef.current.layout();
        }
      });

      // Set up ResizeObserver for reliable container size detection
      if (containerRef.current) {
        // Clean up existing observer
        if (resizeObserverRef.current) {
          resizeObserverRef.current.disconnect();
        }

        resizeObserverRef.current = new ResizeObserver(() => {
          if (editorRef.current) {
            // Use requestAnimationFrame to avoid layout thrashing
            requestAnimationFrame(() => {
              if (editorRef.current) {
                editorRef.current.layout();
              }
            });
          }
        });

        resizeObserverRef.current.observe(containerRef.current);
      }
    },
    [],
  );

  // Additional window resize handler as fallback
  useLayoutEffect(() => {
    const handleResize = () => {
      if (editorRef.current) {
        requestAnimationFrame(() => {
          if (editorRef.current) {
            editorRef.current.layout();
          }
        });
      }
    };

    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  // Force layout update when component is moved to a new container
  useLayoutEffect(() => {
    if (editorRef.current) {
      // Small delay to ensure DOM has updated
      const timeoutId = setTimeout(() => {
        if (editorRef.current) {
          editorRef.current.layout();
        }
      }, 50);

      return () => clearTimeout(timeoutId);
    }
  });

  useEffect(() => {
    if (mode === 'create') {
      // Set up a template for new resource with helpful comments
      const templateYaml = `# You can create any Kubernetes resource here
# Common examples:
# - ConfigMap, Secret, Service, Deployment, StatefulSet, DaemonSet
# - Ingress, NetworkPolicy, PersistentVolumeClaim
# - ServiceAccount, Role, RoleBinding
# 
# Tip: Delete these comments and start with your resource definition
# Press Cmd+S (or Ctrl+S) to create the resource

apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  namespace: default
data:
  key: value

---
# You can also create multiple resources at once by separating with ---
# apiVersion: v1
# kind: Service
# metadata:
#   name: my-service
#   namespace: default
# spec:
#   selector:
#     app: my-app
#   ports:
#   - port: 80
#     targetPort: 8080`;
      
      setYamlContent(templateYaml);
      setIsDirty(true); // Start as dirty for create mode
      setError(null);
    } else if (resource && resource._isHelmValues) {
      // Special handling for Helm values editing
      const fetchHelmValues = async () => {
        try {
          const values = await api.getHelmReleaseValues(cluster, resource._helmReleaseNamespace, resource._helmReleaseName, false);
          const yamlStr = yaml.dump(values, { indent: 2, lineWidth: -1 });
          setYamlContent(yamlStr);
          setIsDirty(false);
          setError(null);
        } catch (error: any) {
          console.warn('Failed to fetch Helm values:', error);
          setYamlContent(resource.spec?.values || '');
          setIsDirty(false);
          setError(null);
        }
      };
      fetchHelmValues();
    } else if (resource) {
      // Always fetch fresh resource data when mounting the editor
      const fetchFreshResource = async () => {
        try {
          const group = resource.apiVersion?.includes('/')
            ? resource.apiVersion.split('/')[0]
            : '';
          const version = resource.apiVersion?.includes('/')
            ? resource.apiVersion.split('/')[1]
            : resource.apiVersion;
          const namespace = resource.metadata?.namespace || '';
          const name = resource.metadata?.name;
          const kind = resource.kind;

          // Fetch the latest version
          const freshResource = await api.getResourceDetails(
            cluster,
            group,
            version,
            kind,
            namespace,
            name,
          );

          const resourceCopy = JSON.parse(JSON.stringify(freshResource));
          delete resourceCopy.metadata?.managedFields;
          delete resourceCopy.metadata?.uid;
          delete resourceCopy.metadata?.selfLink;
          delete resourceCopy.events;
          const yamlStr = yaml.dump(resourceCopy, {
            lineWidth: -1,
            noRefs: true,
            sortKeys: false,
          });
          setYamlContent(yamlStr);
          setIsDirty(false);
          setError(null);
        } catch (error: any) {
          // If fetch fails, fall back to the provided resource
          console.warn('Failed to fetch fresh resource, using provided data:', error);
          const resourceCopy = JSON.parse(JSON.stringify(resource));
          delete resourceCopy.metadata?.managedFields;
          delete resourceCopy.metadata?.uid;
          delete resourceCopy.metadata?.selfLink;
          delete resourceCopy.events;
          const yamlStr = yaml.dump(resourceCopy, {
            lineWidth: -1,
            noRefs: true,
            sortKeys: false,
          });
          setYamlContent(yamlStr);
          setIsDirty(false);
          setError(null);
        }
      };

      fetchFreshResource();
    }
  }, [resource, cluster, mode]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      // Clean up ResizeObserver
      if (resizeObserverRef.current) {
        resizeObserverRef.current.disconnect();
        resizeObserverRef.current = null;
      }

      // Clean up editor reference
      editorRef.current = null;
      
      // Clean up success timeout
      if (successTimeoutRef.current) {
        clearTimeout(successTimeoutRef.current);
      }
    };
  }, []);

  useEffect(() => {
    const t = setTimeout(() => {
      let err: { message: string; line?: number } | null = null;
      try { yaml.loadAll(yamlContent); } catch (e: any) { err = { message: e.reason || e.message, line: e.mark ? e.mark.line + 1 : undefined }; }
      setYamlError(err);
      const monaco = monacoRef.current, model = editorRef.current?.getModel();
      if (monaco && model) {
        monaco.editor.setModelMarkers(model, 'yaml', err?.line ? [{
          severity: monaco.MarkerSeverity.Error, message: err.message,
          startLineNumber: err.line, startColumn: 1, endLineNumber: err.line, endColumn: model.getLineMaxColumn(err.line),
        }] : []);
      }
    }, 150);
    return () => clearTimeout(t);
  }, [yamlContent]);

  const handleEditorChange = (value: string | undefined) => {
    if (value !== undefined) {
      setYamlContent(value);
      setIsDirty(true);
      setError(null);
      setSuccess(null);
    }
  };

  const validateYaml = (content: string): boolean => {
    try {
      // Use loadAll to validate all documents in multi-document YAML
      yaml.loadAll(content);
      return true;
    } catch (e: any) {
      setError(`Invalid YAML: ${e.message}`);
      return false;
    }
  };

  const showSuccess = (message: string) => {
    setSuccess(message);
    setError(null);
    
    // Clear any existing timeout
    if (successTimeoutRef.current) {
      clearTimeout(successTimeoutRef.current);
    }
    
    // Auto-dismiss after 5 seconds
    successTimeoutRef.current = setTimeout(() => {
      setSuccess(null);
    }, 5000);
  };

  const formatErrorMessage = (message: string): React.ReactNode => {
    // Format validation errors with line breaks
    const validationMatch = message.match(/is invalid: \[(.*)\]$/);
    if (validationMatch) {
      const validationErrors = validationMatch[1].split(', ').map((err, index) => (
        <div key={index} className="error-line">
          • {err}
        </div>
      ));
      const prefix = message.substring(0, validationMatch.index) + 'is invalid:';
      return (
        <>
          <div>{prefix}</div>
          <div className="error-details">{validationErrors}</div>
        </>
      );
    }
    // Format "Failed to save:" errors
    if (message.startsWith('Failed to save: ')) {
      return message.replace('Failed to save: ', '');
    }
    return message;
  };

  const handleDryRun = async () => {
    if (!validateYaml(yamlContent)) {
      return;
    }

    if (!isHelmValues) {
      setError('Dry run is only available for Helm values');
      return;
    }

    setIsDryRunning(true);
    setError(null);
    
    try {
      const parsedValues = yaml.load(yamlContent) as Record<string, any> || {};
      const result = await api.upgradeHelmRelease(cluster, resource._helmReleaseNamespace, resource._helmReleaseName, parsedValues, true);
      showSuccess(`Dry run successful - ${(result.manifest.match(/^kind:/gm) || []).length} resources would be deployed`);
    } catch (err: any) {
      if (err.name === 'YAMLException') {
        setError(`Invalid YAML: ${err.message}`);
      } else {
        setError(err.response?.data?.error || err.message || 'Dry run failed');
      }
    } finally {
      setIsDryRunning(false);
    }
  };

  const handleSave = async () => {
    if (!validateYaml(yamlContent)) {
      return;
    }

    setIsSaving(true);
    
    try {
      if (mode === 'create') {
        // For create mode, simply create the resource
        const response = await api.createResource(
          cluster,
          yamlContent,
        );

        // Handle response based on whether single or multiple resources were created
        let createdResource;
        let successMsg;
        
        if (response.created && response.count) {
          // Multiple resources created
          const resourceList = response.created.map((res: any) => 
            `${res.kind}/${res.metadata?.name || 'unnamed'}`
          ).join(', ');
          successMsg = response.count > 3 
            ? `Successfully created ${response.count} resources`
            : `Successfully created: ${resourceList}`;
          console.log(successMsg);
          showSuccess(successMsg);
          
          // Don't update the editor content for multiple resources
          setIsDirty(false);
        } else if (response.created && response.errors) {
          // Partial success
          successMsg = `Created ${response.created.length} resources with ${response.errors.length} errors`;
          console.warn(successMsg, response.errors);
          setError(`Partial success: ${response.errors.join('; ')}`);
        } else {
          // Single resource created
          createdResource = response;
          const resourceCopy = JSON.parse(JSON.stringify(createdResource));
          delete resourceCopy.metadata?.managedFields;
          delete resourceCopy.metadata?.uid;
          delete resourceCopy.metadata?.selfLink;
          delete resourceCopy.events;
          const yamlStr = yaml.dump(resourceCopy, {
            lineWidth: -1,
            noRefs: true,
            sortKeys: false,
          });
          setYamlContent(yamlStr);
          
          setIsDirty(false);
          
          successMsg = `Created ${createdResource.kind} "${createdResource.metadata?.name}" successfully`;
          console.log(successMsg);
          showSuccess(successMsg);
        }

        if (onSave) {
          onSave(yamlContent);
        }
        
      } else if (resource._isHelmValues) {
        // Special handling for Helm values upgrade
        try {
          const parsedValues = yaml.load(yamlContent) as Record<string, any> || {};
          await api.upgradeHelmRelease(cluster, resource._helmReleaseNamespace, resource._helmReleaseName, parsedValues, false);
          
          setIsDirty(false);
          const successMsg = `Successfully upgraded Helm release "${resource._helmReleaseName}"`;
          console.log(successMsg);
          showSuccess(successMsg);
          
          if (onSave) {
            onSave(yamlContent);
          }
        } catch (err: any) {
          const errorMsg = err.response?.data?.error || err.message || 'Failed to upgrade Helm release';
          console.error('[YamlEditor] Helm upgrade failed:', errorMsg);
          setError(`Failed to upgrade: ${errorMsg}`);
        }
      } else {
        // For edit mode, parse the YAML (should be single document)
        const parsedYaml = yaml.load(yamlContent) as any;
        
        // Use the existing update logic with retries
        const maxRetries = 3;
        let retryCount = 0;
        
        while (retryCount < maxRetries) {
          try {
            const group = parsedYaml.apiVersion?.includes('/')
              ? parsedYaml.apiVersion.split('/')[0]
              : '';
            const version = parsedYaml.apiVersion?.includes('/')
              ? parsedYaml.apiVersion.split('/')[1]
              : parsedYaml.apiVersion;
            const namespace = parsedYaml.metadata?.namespace || '';
            const name = parsedYaml.metadata?.name;

            const kind = parsedYaml.kind;

            // Always fetch the latest resource version before updating
            api.invalidateCache();
            const latestResource = await api.getResourceDetails(
              cluster,
              group,
              version,
              kind,
              namespace,
              name,
            );

            // Update metadata from the latest version
            parsedYaml.metadata.resourceVersion =
              latestResource.metadata.resourceVersion;
            parsedYaml.metadata.generation = latestResource.metadata.generation;
            parsedYaml.metadata.uid = latestResource.metadata.uid;

            const updatedYamlContent = yaml.dump(parsedYaml, {
              lineWidth: -1,
              noRefs: true,
              sortKeys: false,
            });

            const updatedResource = await api.updateResource(
              cluster,
              updatedYamlContent,
            );

            // Successfully updated - update the editor with the new version
            const resourceCopy = JSON.parse(JSON.stringify(updatedResource));
            delete resourceCopy.metadata?.managedFields;
            delete resourceCopy.metadata?.uid;
            delete resourceCopy.metadata?.selfLink;
            delete resourceCopy.events;
            const yamlStr = yaml.dump(resourceCopy, {
              lineWidth: -1,
              noRefs: true,
              sortKeys: false,
            });
            setYamlContent(yamlStr);

            updateDetailData(updatedResource);

            setIsDirty(false);
            showSuccess(`Updated ${updatedResource.kind} "${updatedResource.metadata?.name}" successfully`);
            if (onSave) {
              onSave(yamlStr);
            }
            
            // Success - break out of retry loop
            break;
          } catch (e: any) {
            const errorMessage =
              e.response?.data?.error || e.message || 'Failed to update resource';
            
            // Check if it's a conflict error
            if (errorMessage.includes('the object has been modified') && retryCount < maxRetries - 1) {
              retryCount++;
              // Exponential backoff: wait 100ms, 200ms, 400ms
              await new Promise(resolve => setTimeout(resolve, 100 * Math.pow(2, retryCount - 1)));
              console.log(`Retrying save due to version conflict (attempt ${retryCount + 1}/${maxRetries})`);
              continue;
            }
            
            // If not a conflict or max retries reached, show error
            if (errorMessage.includes('the object has been modified')) {
              setError(`Failed to save: The resource was modified by another process. Please refresh the editor and try again.`);
            } else {
              setError(`Failed to save: ${errorMessage}`);
            }
            break;
          }
        }
      }
    } catch (e: any) {
      const errorMessage =
        e.response?.data?.error || e.message || 'Failed to save resource';
      setError(`Failed to save: ${errorMessage}`);
    }
    
    setIsSaving(false);
  };

  const handleRefresh = async () => {
    // Refresh doesn't make sense for create mode
    if (mode === 'create') {
      return;
    }

    if (isDirty) {
      const confirmed = window.confirm('You have unsaved changes. Refreshing will discard them. Continue?');
      if (!confirmed) return;
    }

    setIsRefreshing(true);
    try {
      const group = resource.apiVersion?.includes('/')
        ? resource.apiVersion.split('/')[0]
        : '';
      const version = resource.apiVersion?.includes('/')
        ? resource.apiVersion.split('/')[1]
        : resource.apiVersion;
      const namespace = resource.metadata?.namespace || '';
      const name = resource.metadata?.name;
      const kind = resource.kind;

      // Invalidate cache and fetch fresh data
      api.invalidateCache();
      const freshResource = await api.getResourceDetails(
        cluster,
        group,
        version,
        kind,
        namespace,
        name,
      );

      const resourceCopy = JSON.parse(JSON.stringify(freshResource));
      delete resourceCopy.metadata?.managedFields;
      delete resourceCopy.metadata?.uid;
      delete resourceCopy.metadata?.selfLink;
      delete resourceCopy.events;
      const yamlStr = yaml.dump(resourceCopy, {
        lineWidth: -1,
        noRefs: true,
        sortKeys: false,
      });
      setYamlContent(yamlStr);
      setIsDirty(false);
      setError(null);
      setSuccess(null);

      // Update the detail data in the store
      updateDetailData(freshResource);
    } catch (error: any) {
      setError(`Failed to refresh: ${error.message}`);
    } finally {
      setIsRefreshing(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 's') {
      e.preventDefault();
      if (isDirty && !isSaving) {
        handleSave();
      }
    }
    // Add refresh shortcut
    if ((e.metaKey || e.ctrlKey) && e.key === 'r') {
      e.preventDefault();
      handleRefresh();
    }
    // Escape to dismiss error or success
    if (e.key === 'Escape') {
      if (error) {
        e.preventDefault();
        setError(null);
      } else if (success) {
        e.preventDefault();
        setSuccess(null);
        if (successTimeoutRef.current) {
          clearTimeout(successTimeoutRef.current);
        }
      }
    }
  };

  const handleTemplateSelect = (template: string) => {
    setYamlContent(template);
    setIsDirty(true);
    setError(null);
    setSuccess(null);
  };

  const isLoading = mode === 'edit' && !yamlContent && !error;
  const canSave = isDirty && !isSaving && !isDryRunning && !yamlError;
  const saveLabel = isSaving ? (isHelmValues ? 'Upgrading…' : 'Saving…') : mode === 'create' ? 'Create' : isHelmValues ? 'Upgrade' : 'Apply';
  const name = isHelmValues ? resource._helmReleaseName : resource?.metadata?.name;
  const namespace = isHelmValues ? resource._helmReleaseNamespace : resource?.metadata?.namespace;

  return (
    <div className="yaml-editor-container" onKeyDown={handleKeyDown} ref={containerRef}>
      <div className="yaml-editor-toolbar">
        {mode === 'create' ? (
          <div className="toolbar-resource-selector">
            <ResourceSelector cluster={cluster} onResourceSelect={handleTemplateSelect} />
          </div>
        ) : (
          <div className="yaml-editor-identity">
            <span className="yaml-editor-kind">{isHelmValues ? 'Helm values' : resource?.kind}</span>
            <span className="yaml-editor-name" title={name}>{name}</span>
            {namespace && <span className="yaml-editor-namespace">{namespace}</span>}
          </div>
        )}
        <div className="toolbar-actions">
          {isDirty && <span className="unsaved-indicator"><i />Unsaved changes</span>}
          {mode === 'edit' && !isHelmValues && (
            <button className="yaml-btn" onClick={handleRefresh} disabled={isRefreshing || isSaving} title="Reload from cluster">
              <UpdateIcon className={isRefreshing ? 'spin' : undefined} />{isRefreshing ? 'Refreshing…' : 'Refresh'}<Kbd k="R" />
            </button>
          )}
          {isHelmValues && (
            <button className="yaml-btn" onClick={handleDryRun} disabled={!canSave} title="Test upgrade without applying changes">
              <PlayIcon />{isDryRunning ? 'Testing…' : 'Dry run'}
            </button>
          )}
          <button className="yaml-btn primary" onClick={handleSave} disabled={!canSave} title={yamlError ? 'Fix YAML errors before applying' : undefined}>
            <CheckIcon />{saveLabel}<Kbd k="S" />
          </button>
        </div>
      </div>
      {error && (
        <div className="yaml-editor-banner error" role="alert">
          <ExclamationTriangleIcon />
          <div className="banner-message">{formatErrorMessage(error)}</div>
          <button className="banner-dismiss" onClick={() => setError(null)} aria-label="Dismiss error"><Cross2Icon /></button>
        </div>
      )}
      {success && (
        <div className="yaml-editor-banner success" role="status">
          <CheckCircledIcon />
          <div className="banner-message">{success}</div>
          <button className="banner-dismiss" onClick={() => { setSuccess(null); if (successTimeoutRef.current) clearTimeout(successTimeoutRef.current); }} aria-label="Dismiss"><Cross2Icon /></button>
        </div>
      )}
      <div className={`yaml-editor-body${isLoading || isRefreshing ? ' loading' : ''}`}>
        <Editor
          height="100%"
          width="100%"
          language="yaml"
          theme={KANIVET_MONACO_THEME}
          value={yamlContent}
          onChange={handleEditorChange}
          beforeMount={handleEditorWillMount}
          onMount={handleEditorMount}
          loading={<div className="yaml-editor-placeholder">Loading editor…</div>}
          options={{
            minimap: { enabled: false },
            fontSize: 13,
            lineHeight: 20,
            fontFamily: "'JetBrains Mono', 'SF Mono', 'Cascadia Code', 'Fira Code', 'Monaco', 'Menlo', monospace",
            fontLigatures: true,
            wordWrap: 'on',
            tabSize: 2,
            insertSpaces: true,
            padding: { top: 12, bottom: 12 },
            lineNumbersMinChars: 4,
            lineDecorationsWidth: 12,
            glyphMargin: false,
            folding: true,
            showFoldingControls: 'mouseover',
            renderLineHighlight: 'all',
            renderWhitespace: 'selection',
            guides: { indentation: true, highlightActiveIndentation: true, bracketPairs: false },
            bracketPairColorization: { enabled: false },
            stickyScroll: { enabled: true },
            smoothScrolling: true,
            cursorBlinking: 'smooth',
            cursorSmoothCaretAnimation: 'on',
            scrollBeyondLastLine: false,
            automaticLayout: false,
            overviewRulerBorder: false,
            hideCursorInOverviewRuler: true,
            scrollbar: { vertical: 'visible', horizontal: 'visible', verticalScrollbarSize: 8, horizontalScrollbarSize: 8, alwaysConsumeMouseWheel: false },
            fixedOverflowWidgets: true,
          }}
        />
      </div>
      <div className="yaml-editor-statusbar">
        <span>YAML</span>
        <span>Ln {cursor.line}, Col {cursor.col}</span>
        <span>{yamlContent.split('\n').length} lines</span>
        <span className="statusbar-spacer" />
        {yamlError ? (
          <button className="statusbar-status invalid" onClick={() => yamlError.line && editorRef.current?.revealLineInCenter(yamlError.line)} title={yamlError.message}>
            <CrossCircledIcon />{yamlError.line ? `Line ${yamlError.line}: ` : ''}{yamlError.message}
          </button>
        ) : (
          <span className="statusbar-status valid"><CheckCircledIcon />Valid YAML</span>
        )}
      </div>
    </div>
  );
};

export default YamlEditor;
