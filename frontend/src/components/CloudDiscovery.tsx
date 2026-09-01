import React, { useState, useEffect, useCallback, useRef } from 'react';
import cloudService from '../services/cloudService';
import { useStore } from '../store';
import { SSOSession } from '../store/types';
import {
  CloudProvider,
  AWSProfile,
  GCPProject,
  AzureSubscription,
  DiscoveredCluster,
  CloudAuthStatus,
  BatchImportJob,
  SSOAccount,
  DiscoveryProgress,
} from '../types/cloud';
import {
  LockClosedIcon,
  MagnifyingGlassIcon,
  DownloadIcon,
  CheckIcon,
  ExclamationTriangleIcon,
  DrawingPinIcon,
  BookmarkIcon,
  CheckCircledIcon,
  ReloadIcon,
  PlusIcon,
  TrashIcon,
  ClockIcon,
  ChevronRightIcon,
  CubeIcon,
  Pencil1Icon,
} from '@radix-ui/react-icons';
import AWSIcon from './AWSIcon';
import GCPIcon from './GCPIcon';
import AzureIcon from './AzureIcon';
import './CloudDiscovery.css';

interface CloudDiscoveryProps {
  onClusterImported: () => void;
  onClose?: () => void;
}

type TabId = 'aws' | 'gcp' | 'azure';

const STORAGE_KEY = 'cloud-discovery-state';

export const CloudDiscovery: React.FC<CloudDiscoveryProps> = ({ onClusterImported }) => {
  const {
    ssoSessions,
    loadSsoSessions,
    addSsoSession,
    removeSsoSession: removeStoreSsoSession,
    updateSsoSessionLabel,
    refreshSsoSession,
  } = useStore();

  const [activeTab, setActiveTab] = useState<TabId>('aws');
  const [authStatus, setAuthStatus] = useState<CloudAuthStatus>({ aws: false, gcp: false, azure: false });
  const [loading, setLoading] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [importing, setImporting] = useState<string | null>(null);
  const [batchImporting, setBatchImporting] = useState(false);
  const [batchProgress, setBatchProgress] = useState<{ current: number; total: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  const [awsProfiles, setAwsProfiles] = useState<AWSProfile[]>([]);
  const [profilesLoading, setProfilesLoading] = useState(true);
  const [selectedProfiles, setSelectedProfiles] = useState<Set<string>>(new Set());
  const [selectedSsoSessions, setSelectedSsoSessions] = useState<Set<string>>(new Set());
  const [ssoAccounts, setSsoAccounts] = useState<Map<string, SSOAccount[]>>(new Map());
  const [selectedAccounts, setSelectedAccounts] = useState<Map<string, Set<string>>>(new Map());
  const [loadingAccounts, setLoadingAccounts] = useState<Set<string>>(new Set());
  const [expandedSessions, setExpandedSessions] = useState<Set<string>>(new Set());
  const [editingSessionUrl, setEditingSessionUrl] = useState<string | null>(null);
  const [editingLabel, setEditingLabel] = useState<string>('');
  const [newSsoUrl, setNewSsoUrl] = useState<string>('');
  const [newSsoRegion, setNewSsoRegion] = useState<string>('us-east-1');
  const [showAddSso, setShowAddSso] = useState(false);
  const [expandedProfileGroups, setExpandedProfileGroups] = useState<Set<string>>(new Set(['config', 'credentials']));

  const [gcpProjects, setGcpProjects] = useState<GCPProject[]>([]);
  const [selectedProjects, setSelectedProjects] = useState<Set<string>>(new Set());
  const [azureSubs, setAzureSubs] = useState<AzureSubscription[]>([]);
  const [selectedSubs, setSelectedSubs] = useState<Set<string>>(new Set());
  const [discoveredClusters, setDiscoveredClusters] = useState<DiscoveredCluster[]>([]);
  const [lastDiscoveredAt, setLastDiscoveredAt] = useState<number | null>(null);
  const [backgroundDiscovering, setBackgroundDiscovering] = useState(false);
  const [newClustersCount, setNewClustersCount] = useState(0);
  const [importJobId, setImportJobId] = useState<string | null>(null);
  const [discoveryProgress, setDiscoveryProgress] = useState<DiscoveryProgress | null>(null);
  const [selectedRoles, setSelectedRoles] = useState<Map<string, string>>(new Map());
  const stopDiscoveryRef = useRef<(() => void)[]>([]);
  const initialLoadDoneRef = useRef(false);

  const loadPersistedState = useCallback(async () => {
    const start = performance.now();
    try {
      await loadSsoSessions();
      console.log(`[CloudDiscovery] loadSsoSessions took ${(performance.now() - start).toFixed(0)}ms`);
      const stored = localStorage.getItem(STORAGE_KEY);
      const localState = stored ? JSON.parse(stored) : { discoveredClusters: [], lastDiscoveredAt: null };
      const importedIds = await cloudService.getImportedClusterIDs();
      console.log(`[CloudDiscovery] getImportedClusterIDs took ${(performance.now() - start).toFixed(0)}ms`);
      const importedSet = new Set(importedIds);

      if (localState.lastDiscoveredAt) setLastDiscoveredAt(localState.lastDiscoveredAt);
      if (localState.discoveredClusters) {
        const clustersWithImportStatus = localState.discoveredClusters.map((c: DiscoveredCluster) => ({
          ...c,
          isImported: importedSet.has(c.name) || importedSet.has(`eks-${c.name}`) || importedSet.has(c.id),
        }));
        setDiscoveredClusters(clustersWithImportStatus);
      }
      initialLoadDoneRef.current = true;
      console.log(`[CloudDiscovery] Total loadPersistedState took ${(performance.now() - start).toFixed(0)}ms`);
    } catch (err) {
      console.error('Failed to load persisted state:', err);
      initialLoadDoneRef.current = true;
    }
  }, [loadSsoSessions]);

  const persistState = useCallback((clusters: DiscoveredCluster[], timestamp: number | null) => {
    if (!initialLoadDoneRef.current) return;
    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      const existing = stored ? JSON.parse(stored) : {};
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        ...existing,
        discoveredClusters: clusters,
        lastDiscoveredAt: timestamp,
      }));
    } catch (err) {
      console.error('Failed to persist state:', err);
    }
  }, []);

  useEffect(() => {
    loadPersistedState();
    loadAuthStatus();
    loadAWSProfiles();
    return () => {
      stopDiscoveryRef.current.forEach(stop => stop());
    };
  }, [loadPersistedState]);

  useEffect(() => {
    if (activeTab === 'gcp' && authStatus.gcp) loadGCPProjects();
  }, [activeTab, authStatus.gcp]);

  useEffect(() => {
    if (activeTab === 'azure' && authStatus.azure) loadAzureSubscriptions();
  }, [activeTab, authStatus.azure]);

  useEffect(() => {
    persistState(discoveredClusters, lastDiscoveredAt);
  }, [discoveredClusters, lastDiscoveredAt, persistState]);

  const ssoSessionsInitializedRef = useRef(false);
  useEffect(() => {
    if (!ssoSessionsInitializedRef.current && ssoSessions.length > 0) {
      ssoSessionsInitializedRef.current = true;
      setSelectedSsoSessions(new Set(ssoSessions.map(s => s.startUrl)));
    }
  }, [ssoSessions]);

  const loadAuthStatus = async () => {
    try {
      const status = await cloudService.getAuthStatus();
      setAuthStatus(status);
    } catch (err) {
      console.error('Failed to load auth status:', err);
    }
  };

  const loadAWSProfiles = async () => {
    setProfilesLoading(true);
    try {
      const profiles = await cloudService.listAWSProfiles();
      setAwsProfiles(profiles);
    } catch (err) {
      console.error('Failed to load AWS profiles:', err);
    } finally {
      setProfilesLoading(false);
    }
  };

  const loadGCPProjects = async () => {
    try {
      const projects = await cloudService.listGCPProjects();
      setGcpProjects(projects);
      if (projects.length > 0 && selectedProjects.size === 0) {
        setSelectedProjects(new Set([projects[0].id]));
      }
    } catch (err) {
      console.error('Failed to load GCP projects:', err);
    }
  };

  const loadAzureSubscriptions = async () => {
    try {
      const subs = await cloudService.listAzureSubscriptions();
      setAzureSubs(subs);
      if (subs.length > 0 && selectedSubs.size === 0) {
        setSelectedSubs(new Set([subs[0].id]));
      }
    } catch (err) {
      console.error('Failed to load Azure subscriptions:', err);
    }
  };

  const toggleProfile = (name: string) => {
    setSelectedProfiles(prev => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };

  const toggleProject = (id: string) => {
    setSelectedProjects(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSub = (id: string) => {
    setSelectedSubs(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const handleAddSsoSession = async () => {
    if (!newSsoUrl) return;
    setLoading(true);
    setError(null);
    try {
      await cloudService.startAWSSSOLogin(newSsoUrl, newSsoRegion);
      addSsoSession({
        startUrl: newSsoUrl,
        region: newSsoRegion,
        expiresAt: Date.now() + 8 * 60 * 60 * 1000,
        label: new URL(newSsoUrl).hostname.split('.')[0],
      });
      setSelectedSsoSessions(prev => new Set([...prev, newSsoUrl]));
      setNewSsoUrl('');
      setShowAddSso(false);
      setSuccessMessage('SSO authentication successful!');
    } catch (err: any) {
      setError(err.response?.data?.error || err.message || 'SSO login failed');
    } finally {
      setLoading(false);
    }
  };

  const reconnectSsoSession = async (session: SSOSession) => {
    setLoading(true);
    setError(null);
    try {
      await refreshSsoSession(session.startUrl, session.region);
      setSuccessMessage('Session refreshed!');
    } catch (err: any) {
      setError(err.response?.data?.error || err.message || 'Reconnect failed');
    } finally {
      setLoading(false);
    }
  };

  const handleRemoveSsoSession = (startUrl: string) => {
    removeStoreSsoSession(startUrl);
    setSelectedSsoSessions(prev => {
      const next = new Set(prev);
      next.delete(startUrl);
      return next;
    });
    setSsoAccounts(prev => {
      const next = new Map(prev);
      next.delete(startUrl);
      return next;
    });
    setSelectedAccounts(prev => {
      const next = new Map(prev);
      next.delete(startUrl);
      return next;
    });
  };

  const startEditingLabel = (session: SSOSession) => {
    setEditingSessionUrl(session.startUrl);
    setEditingLabel(session.label || '');
  };

  const saveSessionLabel = () => {
    if (!editingSessionUrl) return;
    const newLabel = editingLabel.trim() || new URL(editingSessionUrl).hostname.split('.')[0];
    updateSsoSessionLabel(editingSessionUrl, newLabel);
    setEditingSessionUrl(null);
    setEditingLabel('');
  };

  const cancelEditingLabel = () => {
    setEditingSessionUrl(null);
    setEditingLabel('');
  };

  const loadSSOAccounts = async (startUrl: string) => {
    if (ssoAccounts.has(startUrl) || loadingAccounts.has(startUrl)) return;
    setLoadingAccounts(prev => new Set([...prev, startUrl]));
    try {
      const accounts = await cloudService.getAWSSSOAccounts(startUrl);
      setSsoAccounts(prev => new Map(prev).set(startUrl, accounts));
      setSelectedAccounts(prev => new Map(prev).set(startUrl, new Set(accounts.map(a => a.accountId))));
    } catch (err) {
      console.error('Failed to load SSO accounts:', err);
    } finally {
      setLoadingAccounts(prev => {
        const next = new Set(prev);
        next.delete(startUrl);
        return next;
      });
    }
  };

  const toggleSessionExpanded = (startUrl: string) => {
    setExpandedSessions(prev => {
      const next = new Set(prev);
      if (next.has(startUrl)) next.delete(startUrl);
      else {
        next.add(startUrl);
        loadSSOAccounts(startUrl);
      }
      return next;
    });
  };

  const toggleAccount = (startUrl: string, accountId: string) => {
    setSelectedAccounts(prev => {
      const next = new Map(prev);
      const accounts = next.get(startUrl) || new Set();
      const updated = new Set(accounts);
      if (updated.has(accountId)) updated.delete(accountId);
      else updated.add(accountId);
      next.set(startUrl, updated);
      return next;
    });
  };

  const toggleAllAccounts = (startUrl: string) => {
    const accounts = ssoAccounts.get(startUrl) || [];
    const selected = selectedAccounts.get(startUrl) || new Set();
    const allSelected = accounts.length > 0 && selected.size === accounts.length;
    setSelectedAccounts(prev => {
      const next = new Map(prev);
      next.set(startUrl, allSelected ? new Set() : new Set(accounts.map(a => a.accountId)));
      return next;
    });
  };

  const toggleProfileGroup = (source: string) => {
    setExpandedProfileGroups(prev => {
      const next = new Set(prev);
      if (next.has(source)) next.delete(source);
      else next.add(source);
      return next;
    });
  };

  const toggleAllProfilesInGroup = (source: string) => {
    const profilesInGroup = awsProfiles.filter(p => p.source === source);
    const allSelected = profilesInGroup.every(p => selectedProfiles.has(p.name));
    setSelectedProfiles(prev => {
      const next = new Set(prev);
      profilesInGroup.forEach(p => {
        if (allSelected) next.delete(p.name);
        else next.add(p.name);
      });
      return next;
    });
  };

  const isSessionExpired = (session: SSOSession) => Date.now() > session.expiresAt;
  const isSessionExpiringSoon = (session: SSOSession) => {
    const timeLeft = session.expiresAt - Date.now();
    return timeLeft > 0 && timeLeft < 30 * 60 * 1000;
  };

  const formatTimeLeft = (expiresAt: number) => {
    const ms = expiresAt - Date.now();
    if (ms <= 0) return 'Expired';
    const hours = Math.floor(ms / (1000 * 60 * 60));
    const mins = Math.floor((ms % (1000 * 60 * 60)) / (1000 * 60));
    if (hours > 0) return `${hours}h ${mins}m left`;
    return `${mins}m left`;
  };

  const handleLoginGCP = async () => {
    setLoading(true);
    setError(null);
    try {
      await cloudService.loginGCP();
      setSuccessMessage('GCP authentication successful!');
      await loadAuthStatus();
    } catch (err: any) {
      setError(err.response?.data?.error || err.message || 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  const handleLoginAzure = async () => {
    setLoading(true);
    setError(null);
    try {
      await cloudService.loginAzure();
      setSuccessMessage('Azure authentication successful!');
      await loadAuthStatus();
    } catch (err: any) {
      setError(err.response?.data?.error || err.message || 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  const handleDiscover = async () => {
    stopDiscoveryRef.current.forEach(stop => stop());
    stopDiscoveryRef.current = [];

    setDiscovering(true);
    setError(null);
    setSuccessMessage(null);
    setDiscoveryProgress(null);
    setDiscoveredClusters([]);

    const importedIds = await cloudService.getImportedClusterIDs();
    const importedSet = new Set(importedIds);
    const allClusters: DiscoveredCluster[] = [];
    const seenIds = new Set<string>();
    let pendingStreams = 0;

    const handleStreamComplete = () => {
      pendingStreams--;
      if (pendingStreams <= 0) {
        setDiscovering(false);
        setDiscoveryProgress(null);
        setLastDiscoveredAt(Date.now());
        if (allClusters.length === 0) {
          setError('No clusters found');
        } else {
          setSuccessMessage(`Found ${allClusters.length} cluster(s)`);
        }
      }
    };

    const createStreamHandler = (ssoStartUrl?: string, profile?: string) => (event: any) => {
      if (event.type === 'cluster' && event.cluster) {
        const cluster = { ...event.cluster, ssoStartUrl, profile };
        if (!seenIds.has(cluster.id)) {
          seenIds.add(cluster.id);
          cluster.isImported = importedSet.has(cluster.name) || importedSet.has(`eks-${cluster.name}`) || importedSet.has(cluster.id);
          allClusters.push(cluster);
          setDiscoveredClusters([...allClusters]);
        }
      } else if (event.type === 'status_update' && event.cluster) {
        const idx = allClusters.findIndex(c => c.id === event.cluster.id);
        if (idx !== -1) {
          allClusters[idx] = {
            ...allClusters[idx],
            hasAccess: event.cluster.hasAccess,
            accessChecked: event.cluster.accessChecked,
            accessError: event.cluster.accessError,
          };
          setDiscoveredClusters([...allClusters]);
        }
      } else if (event.type === 'progress' && event.progress) {
        setDiscoveryProgress(event.progress);
      } else if (event.type === 'complete') {
        handleStreamComplete();
      } else if (event.type === 'error') {
        console.error('Discovery error:', event.error);
        handleStreamComplete();
      }
    };

    if (activeTab === 'aws') {
      for (const startUrl of selectedSsoSessions) {
        const session = ssoSessions.find(s => s.startUrl === startUrl);
        if (!session || isSessionExpired(session)) continue;
        const accountIds = Array.from(selectedAccounts.get(startUrl) || []);
        if (accountIds.length === 0) continue;
        pendingStreams++;
        const stop = cloudService.discoverClustersStreaming(
          { provider: 'aws', ssoStartUrl: startUrl, accountIds, allRegions: true },
          createStreamHandler(startUrl, undefined)
        );
        stopDiscoveryRef.current.push(stop);
      }
      for (const profileName of selectedProfiles) {
        pendingStreams++;
        const stop = cloudService.discoverClustersStreaming(
          { provider: 'aws', profile: profileName, allRegions: true },
          createStreamHandler(undefined, profileName)
        );
        stopDiscoveryRef.current.push(stop);
      }
    } else if (activeTab === 'gcp') {
      for (const projectId of selectedProjects) {
        pendingStreams++;
        const stop = cloudService.discoverClustersStreaming(
          { provider: 'gcp', projectId },
          createStreamHandler()
        );
        stopDiscoveryRef.current.push(stop);
      }
    } else if (activeTab === 'azure') {
      for (const subscription of selectedSubs) {
        pendingStreams++;
        const stop = cloudService.discoverClustersStreaming(
          { provider: 'azure', subscription },
          createStreamHandler()
        );
        stopDiscoveryRef.current.push(stop);
      }
    }

    if (pendingStreams === 0) {
      setDiscovering(false);
      setError('No sources selected for discovery');
    }
  };

  const handleImport = async (cluster: DiscoveredCluster) => {
    setImporting(cluster.id);
    setError(null);
    try {
      const selectedRole = selectedRoles.get(cluster.id) || cluster.availableRoles?.[0];
      await cloudService.importCluster({
        provider: cluster.provider,
        clusterId: cluster.id,
        name: cluster.name,
        region: cluster.region,
        accountId: cluster.accountId,
        projectId: cluster.projectId,
        resourceGroup: cluster.resourceGroup,
        profile: cluster.profile,
        ssoStartUrl: cluster.ssoStartUrl,
        ssoRoleName: selectedRole,
      });
      setDiscoveredClusters(prev =>
        prev.map(c => c.id === cluster.id ? { ...c, isImported: true } : c)
      );
      onClusterImported();
    } catch (err: any) {
      setError(err.response?.data?.error || err.message || 'Import failed');
    } finally {
      setImporting(null);
    }
  };

  const handleBatchImport = async () => {
    const clustersToImport = discoveredClusters.filter(c => !c.isImported);
    if (clustersToImport.length === 0) return;

    setBatchImporting(true);
    setBatchProgress({ current: 0, total: clustersToImport.length });
    setError(null);
    setSuccessMessage(null);

    try {
      const requests = clustersToImport.map(cluster => ({
        provider: cluster.provider,
        clusterId: cluster.id,
        name: cluster.name,
        region: cluster.region,
        accountId: cluster.accountId,
        projectId: cluster.projectId,
        resourceGroup: cluster.resourceGroup,
        profile: cluster.profile,
        ssoStartUrl: cluster.ssoStartUrl,
        ssoRoleName: selectedRoles.get(cluster.id) || cluster.availableRoles?.[0],
      }));

      const { jobId } = await cloudService.batchImportClusters(requests);
      setImportJobId(jobId);
    } catch (err: any) {
      setError(err.response?.data?.error || err.message || 'Batch import failed');
      setBatchImporting(false);
      setBatchProgress(null);
    }
  };

  const handleImportJobComplete = useCallback((job: BatchImportJob) => {
    const successIds = new Set(job.results.filter(r => r.success).map(r => r.clusterId));
    setDiscoveredClusters(prev =>
      prev.map(c => successIds.has(c.id) ? { ...c, isImported: true } : c)
    );
    if (job.failed > 0) {
      const failedNames = job.results.filter(r => !r.success).map(r => r.name).slice(0, 3);
      setError(`${job.failed} cluster(s) failed: ${failedNames.join(', ')}${job.failed > 3 ? '...' : ''}`);
    }
    if (job.successful > 0) {
      setSuccessMessage(`Successfully imported ${job.successful} cluster(s)`);
      onClusterImported();
    }
    setBatchImporting(false);
    setBatchProgress(null);
    setImportJobId(null);
  }, [onClusterImported]);

  useEffect(() => {
    if (!importJobId) return;
    let cancelled = false;
    const pollImportStatus = async () => {
      const job = await cloudService.getBatchImportStatus(importJobId);
      if (!job || cancelled) return;
      setBatchProgress({ current: job.completed, total: job.total });
      if (!job.inProgress) {
        handleImportJobComplete(job);
      }
    };
    pollImportStatus();
    const interval = setInterval(pollImportStatus, 1000);
    return () => { cancelled = true; clearInterval(interval); };
  }, [importJobId, handleImportJobComplete]);

  const runBackgroundDiscovery = useCallback(async () => {
    if (discovering || backgroundDiscovering || !canDiscover()) return;
    setBackgroundDiscovering(true);
    let allClusters: DiscoveredCluster[] = [];
    try {
      for (const startUrl of selectedSsoSessions) {
        const session = ssoSessions.find(s => s.startUrl === startUrl);
        if (!session || isSessionExpired(session)) continue;
        const accountIds = Array.from(selectedAccounts.get(startUrl) || []);
        if (accountIds.length === 0) continue;
        try {
          const clusters = await cloudService.discoverClusters({ provider: 'aws', ssoStartUrl: startUrl, accountIds, allRegions: true });
          allClusters = [...allClusters, ...clusters.map(c => ({ ...c, ssoStartUrl: startUrl }))];
        } catch { /* ignore */ }
      }
      for (const profileName of selectedProfiles) {
        try {
          const clusters = await cloudService.discoverClusters({ provider: 'aws', profile: profileName, allRegions: true });
          allClusters = [...allClusters, ...clusters.map(c => ({ ...c, profile: profileName }))];
        } catch { /* ignore */ }
      }
      const importedIds = await cloudService.getImportedClusterIDs();
      const importedSet = new Set(importedIds);
      const existingIds = new Set(discoveredClusters.map(c => c.id));
      const uniqueClusters = allClusters
        .filter((c, i, arr) => arr.findIndex(x => x.id === c.id) === i)
        .map(c => ({ ...c, isImported: importedSet.has(c.name) || importedSet.has(`eks-${c.name}`) || importedSet.has(c.id) }));
      const newCount = uniqueClusters.filter(c => !existingIds.has(c.id)).length;
      if (newCount > 0) setNewClustersCount(newCount);
      setDiscoveredClusters(uniqueClusters);
      setLastDiscoveredAt(Date.now());
    } catch { /* ignore */ } finally {
      setBackgroundDiscovering(false);
    }
  }, [discovering, backgroundDiscovering, selectedSsoSessions, selectedAccounts, selectedProfiles, ssoSessions, discoveredClusters]);

  const backgroundDiscoveryTriggeredRef = useRef(false);
  useEffect(() => {
    if (backgroundDiscoveryTriggeredRef.current) return;
    if (discoveredClusters.length > 0 && canDiscover()) {
      backgroundDiscoveryTriggeredRef.current = true;
      const timer = setTimeout(() => runBackgroundDiscovery(), 5000);
      return () => clearTimeout(timer);
    }
  }, [discoveredClusters.length, runBackgroundDiscovery]);

  const formatLastDiscovered = (timestamp: number) => {
    const diff = Date.now() - timestamp;
    if (diff < 60000) return 'just now';
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
    return new Date(timestamp).toLocaleDateString();
  };

  const renderProviderIcon = (provider: CloudProvider) => {
    const iconClass = `cloud-cluster-icon ${provider}`;
    switch (provider) {
      case 'aws': return <div className={iconClass}><AWSIcon /></div>;
      case 'gcp': return <div className={iconClass}><GCPIcon /></div>;
      case 'azure': return <div className={iconClass}><AzureIcon /></div>;
    }
  };

  const canDiscover = () => {
    if (activeTab === 'aws') {
      const hasValidSso = Array.from(selectedSsoSessions).some(url => {
        const s = ssoSessions.find(ss => ss.startUrl === url);
        return s && !isSessionExpired(s);
      });
      return hasValidSso || selectedProfiles.size > 0;
    }
    if (activeTab === 'gcp') return selectedProjects.size > 0;
    if (activeTab === 'azure') return selectedSubs.size > 0;
    return false;
  };

  const renderAWSSettings = () => (
    <div className="cloud-settings-panel">
      <div className="cloud-settings-section">
        <div className="cloud-settings-section-header">
          <h3><LockClosedIcon /> SSO Sessions</h3>
          <button className="cloud-icon-btn" onClick={() => setShowAddSso(!showAddSso)} title="Add SSO">
            <PlusIcon />
          </button>
        </div>

        {showAddSso && (
          <div className="cloud-add-sso-form">
            <input
              type="text"
              value={newSsoUrl}
              onChange={(e) => setNewSsoUrl(e.target.value)}
              placeholder="https://my-org.awsapps.com/start"
              className="cloud-input"
            />
            <select value={newSsoRegion} onChange={(e) => setNewSsoRegion(e.target.value)} className="cloud-select">
              <optgroup label="US">
                <option value="us-east-1">us-east-1 (N. Virginia)</option>
                <option value="us-east-2">us-east-2 (Ohio)</option>
                <option value="us-west-1">us-west-1 (N. California)</option>
                <option value="us-west-2">us-west-2 (Oregon)</option>
              </optgroup>
              <optgroup label="Canada">
                <option value="ca-central-1">ca-central-1 (Central)</option>
                <option value="ca-west-1">ca-west-1 (Calgary)</option>
              </optgroup>
              <optgroup label="South America">
                <option value="sa-east-1">sa-east-1 (São Paulo)</option>
              </optgroup>
              <optgroup label="Europe">
                <option value="eu-west-1">eu-west-1 (Ireland)</option>
                <option value="eu-west-2">eu-west-2 (London)</option>
                <option value="eu-west-3">eu-west-3 (Paris)</option>
                <option value="eu-central-1">eu-central-1 (Frankfurt)</option>
                <option value="eu-central-2">eu-central-2 (Zurich)</option>
                <option value="eu-north-1">eu-north-1 (Stockholm)</option>
                <option value="eu-south-1">eu-south-1 (Milan)</option>
                <option value="eu-south-2">eu-south-2 (Spain)</option>
              </optgroup>
              <optgroup label="Asia Pacific">
                <option value="ap-northeast-1">ap-northeast-1 (Tokyo)</option>
                <option value="ap-northeast-2">ap-northeast-2 (Seoul)</option>
                <option value="ap-northeast-3">ap-northeast-3 (Osaka)</option>
                <option value="ap-southeast-1">ap-southeast-1 (Singapore)</option>
                <option value="ap-southeast-2">ap-southeast-2 (Sydney)</option>
                <option value="ap-southeast-3">ap-southeast-3 (Jakarta)</option>
                <option value="ap-southeast-4">ap-southeast-4 (Melbourne)</option>
                <option value="ap-southeast-5">ap-southeast-5 (Malaysia)</option>
                <option value="ap-southeast-6">ap-southeast-6 (New Zealand)</option>
                <option value="ap-southeast-7">ap-southeast-7 (Thailand)</option>
                <option value="ap-south-1">ap-south-1 (Mumbai)</option>
                <option value="ap-south-2">ap-south-2 (Hyderabad)</option>
                <option value="ap-east-1">ap-east-1 (Hong Kong)</option>
                <option value="ap-east-2">ap-east-2 (Taipei)</option>
              </optgroup>
              <optgroup label="Middle East">
                <option value="me-south-1">me-south-1 (Bahrain)</option>
                <option value="me-central-1">me-central-1 (UAE)</option>
              </optgroup>
              <optgroup label="Africa">
                <option value="af-south-1">af-south-1 (Cape Town)</option>
              </optgroup>
              <optgroup label="Israel">
                <option value="il-central-1">il-central-1 (Tel Aviv)</option>
              </optgroup>
              <optgroup label="Mexico">
                <option value="mx-central-1">mx-central-1 (Central)</option>
              </optgroup>
            </select>
            <button className="cloud-btn cloud-btn-primary cloud-btn-sm" onClick={handleAddSsoSession} disabled={loading || !newSsoUrl}>
              {loading ? 'Connecting...' : 'Connect'}
            </button>
          </div>
        )}

        <div className="cloud-sso-list">
          {ssoSessions.length === 0 ? (
            <div className="cloud-empty-hint">No SSO sessions. Click + to add one.</div>
          ) : (
            [...ssoSessions].sort((a, b) => (a.label || '').localeCompare(b.label || '')).map(session => {
              const expired = isSessionExpired(session);
              const expiringSoon = isSessionExpiringSoon(session);
              const isExpanded = expandedSessions.has(session.startUrl);
              const accounts = ssoAccounts.get(session.startUrl) || [];
              const selected = selectedAccounts.get(session.startUrl) || new Set();
              const isLoading = loadingAccounts.has(session.startUrl);
              return (
                <div key={session.startUrl} className={`cloud-sso-item-wrapper ${expired ? 'expired' : ''}`}>
                  <div className={`cloud-sso-item ${expiringSoon ? 'expiring' : ''}`}>
                    {accounts.length > 0 && (
                      <input
                        type="checkbox"
                        checked={accounts.length > 0 && selected.size === accounts.length}
                        ref={(el) => { if (el) el.indeterminate = selected.size > 0 && selected.size < accounts.length; }}
                        onChange={() => toggleAllAccounts(session.startUrl)}
                        onClick={(e) => e.stopPropagation()}
                        disabled={expired}
                      />
                    )}
                    <button
                      className="cloud-icon-btn cloud-expand-btn"
                      onClick={() => !expired && toggleSessionExpanded(session.startUrl)}
                      disabled={expired}
                    >
                      <ChevronRightIcon style={{ transform: isExpanded ? 'rotate(90deg)' : 'none', transition: 'transform 0.15s' }} />
                    </button>
                    <div className="cloud-sso-item-info" onClick={() => !expired && editingSessionUrl !== session.startUrl && toggleSessionExpanded(session.startUrl)}>
                      {editingSessionUrl === session.startUrl ? (
                        <input
                          type="text"
                          className="cloud-sso-label-input"
                          value={editingLabel}
                          onChange={(e) => setEditingLabel(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') saveSessionLabel();
                            else if (e.key === 'Escape') cancelEditingLabel();
                          }}
                          onBlur={saveSessionLabel}
                          onClick={(e) => e.stopPropagation()}
                          autoFocus
                          placeholder="Session alias"
                        />
                      ) : (
                        <span className="cloud-sso-item-label">{session.label || 'SSO'}</span>
                      )}
                      <span className="cloud-sso-item-time">
                        <ClockIcon />
                        {formatTimeLeft(session.expiresAt)}
                      </span>
                      {accounts.length > 0 && (
                        <span className="cloud-sso-account-count">{selected.size}/{accounts.length} accounts</span>
                      )}
                    </div>
                    <div className="cloud-sso-item-actions">
                      {editingSessionUrl !== session.startUrl && (
                        <button
                          className="cloud-icon-btn"
                          onClick={(e) => { e.stopPropagation(); startEditingLabel(session); }}
                          title="Edit alias"
                        >
                          <Pencil1Icon />
                        </button>
                      )}
                      <button
                        className="cloud-icon-btn"
                        onClick={() => reconnectSsoSession(session)}
                        title="Reconnect"
                        disabled={loading}
                      >
                        <ReloadIcon />
                      </button>
                      <button
                        className="cloud-icon-btn cloud-icon-btn-danger"
                        onClick={() => handleRemoveSsoSession(session.startUrl)}
                        title="Remove"
                      >
                        <TrashIcon />
                      </button>
                    </div>
                  </div>
                  {isExpanded && !expired && (
                    <div className="cloud-sso-accounts">
                      {isLoading ? (
                        <div className="cloud-loading-inline cloud-loading-sm">
                          <div className="cloud-spinner"></div>
                          <span>Loading accounts...</span>
                        </div>
                      ) : accounts.length === 0 ? (
                        <div className="cloud-empty-hint">No accounts found</div>
                      ) : (
                        <>
                          {[...accounts].sort((a, b) => a.accountName.localeCompare(b.accountName)).map(account => (
                            <label key={account.accountId} className={`cloud-profile-item ${selected.has(account.accountId) ? 'selected' : ''}`}>
                              <input
                                type="checkbox"
                                checked={selected.has(account.accountId)}
                                onChange={() => toggleAccount(session.startUrl, account.accountId)}
                              />
                              <div className="cloud-profile-content">
                                <span className="cloud-profile-name" title={account.accountName}>{account.accountName}</span>
                                <div className="cloud-profile-meta">
                                  <span className="cloud-badge">SSO</span>
                                  <span className="cloud-profile-region">{account.accountId}</span>
                                </div>
                              </div>
                            </label>
                          ))}
                        </>
                      )}
                    </div>
                  )}
                </div>
              );
            })
          )}
        </div>
      </div>

      <div className="cloud-settings-section">
        <div className="cloud-settings-section-header">
          <h3><AWSIcon /> Profiles</h3>
        </div>
        <div className="cloud-profile-list">
          {profilesLoading ? (
            <div className="cloud-empty-hint">Loading profiles...</div>
          ) : awsProfiles.length === 0 ? (
            <div className="cloud-empty-hint">No AWS profiles found</div>
          ) : (
            <>
              {(['config', 'credentials'] as const).map(source => {
                const profilesInGroup = awsProfiles.filter(p => p.source === source);
                if (profilesInGroup.length === 0) return null;
                const isExpanded = expandedProfileGroups.has(source);
                const selectedCount = profilesInGroup.filter(p => selectedProfiles.has(p.name)).length;
                const sourceLabel = source === 'config' ? '~/.aws/config' : '~/.aws/credentials';
                return (
                  <div key={source} className="cloud-profile-group">
                    <div className="cloud-profile-group-header" onClick={() => toggleProfileGroup(source)}>
                      <input
                        type="checkbox"
                        checked={selectedCount === profilesInGroup.length}
                        ref={(el) => { if (el) el.indeterminate = selectedCount > 0 && selectedCount < profilesInGroup.length; }}
                        onChange={() => toggleAllProfilesInGroup(source)}
                        onClick={(e) => e.stopPropagation()}
                      />
                      <button className="cloud-icon-btn cloud-expand-btn">
                        <ChevronRightIcon style={{ transform: isExpanded ? 'rotate(90deg)' : 'none', transition: 'transform 0.15s' }} />
                      </button>
                      <span className="cloud-profile-group-label">{sourceLabel}</span>
                      <span className="cloud-profile-group-count">{selectedCount}/{profilesInGroup.length}</span>
                    </div>
                    {isExpanded && (
                      <div className="cloud-profile-group-items">
                        {[...profilesInGroup].sort((a, b) => a.name.localeCompare(b.name)).map(p => (
                          <label key={p.name} className={`cloud-profile-item ${selectedProfiles.has(p.name) ? 'selected' : ''}`}>
                            <input
                              type="checkbox"
                              checked={selectedProfiles.has(p.name)}
                              onChange={() => toggleProfile(p.name)}
                            />
                            <div className="cloud-profile-content">
                              <span className="cloud-profile-name" title={p.name}>{p.name}</span>
                              {(p.isSso || p.region) && (
                                <div className="cloud-profile-meta">
                                  {p.isSso && <span className="cloud-badge">SSO</span>}
                                  {p.region && <span className="cloud-profile-region">{p.region}</span>}
                                </div>
                              )}
                            </div>
                          </label>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
            </>
          )}
        </div>
      </div>
    </div>
  );

  const renderGCPSettings = () => (
    <div className="cloud-settings-panel">
      {!authStatus.gcp ? (
        <div className="cloud-auth-prompt-inline">
          <GCPIcon />
          <p>Login to discover GKE clusters</p>
          <button className="cloud-btn cloud-btn-primary" onClick={handleLoginGCP} disabled={loading}>
            {loading ? 'Authenticating...' : 'Login with gcloud'}
          </button>
        </div>
      ) : (
        <div className="cloud-settings-section">
          <div className="cloud-settings-section-header">
            <h3><GCPIcon /> Projects</h3>
          </div>
          <div className="cloud-profile-list">
            {gcpProjects.map(p => (
              <label key={p.id} className={`cloud-profile-item ${selectedProjects.has(p.id) ? 'selected' : ''}`}>
                <input
                  type="checkbox"
                  checked={selectedProjects.has(p.id)}
                  onChange={() => toggleProject(p.id)}
                />
                <span className="cloud-profile-name">{p.name}</span>
                <span className="cloud-profile-region">{p.id}</span>
              </label>
            ))}
          </div>
        </div>
      )}
    </div>
  );

  const renderAzureSettings = () => (
    <div className="cloud-settings-panel">
      {!authStatus.azure ? (
        <div className="cloud-auth-prompt-inline">
          <AzureIcon />
          <p>Login to discover AKS clusters</p>
          <button className="cloud-btn cloud-btn-primary" onClick={handleLoginAzure} disabled={loading}>
            {loading ? 'Authenticating...' : 'Login with Azure CLI'}
          </button>
        </div>
      ) : (
        <div className="cloud-settings-section">
          <div className="cloud-settings-section-header">
            <h3><AzureIcon /> Subscriptions</h3>
          </div>
          <div className="cloud-profile-list">
            {azureSubs.map(s => (
              <label key={s.id} className={`cloud-profile-item ${selectedSubs.has(s.id) ? 'selected' : ''}`}>
                <input
                  type="checkbox"
                  checked={selectedSubs.has(s.id)}
                  onChange={() => toggleSub(s.id)}
                />
                <span className="cloud-profile-name">{s.name}</span>
                <span className={`cloud-badge ${s.state === 'Enabled' ? 'success' : ''}`}>{s.state}</span>
              </label>
            ))}
          </div>
        </div>
      )}
    </div>
  );

  const renderClustersPanel = () => (
    <div className="cloud-clusters-panel">
      <div className="cloud-clusters-header">
        <h3><CubeIcon /> Discovered Clusters</h3>
        <div className="cloud-clusters-header-right">
          {newClustersCount > 0 && (
            <span className="cloud-new-badge" onClick={() => setNewClustersCount(0)}>+{newClustersCount} new</span>
          )}
          {discoveredClusters.length > 0 && (
            <span className="cloud-clusters-count">{discoveredClusters.length}</span>
          )}
        </div>
      </div>

      {lastDiscoveredAt && (
        <div className="cloud-last-discovered">
          <ClockIcon />
          <span>Last discovered {formatLastDiscovered(lastDiscoveredAt)}</span>
          {backgroundDiscovering && <span className="cloud-bg-spinner" />}
        </div>
      )}

      {(error || successMessage) && (
        <div className={`cloud-message ${error ? 'error' : 'success'}`}>
          {error ? <ExclamationTriangleIcon /> : <CheckCircledIcon />}
          {error || successMessage}
        </div>
      )}

      <div className="cloud-clusters-actions">
        <button
          className="cloud-btn cloud-btn-primary"
          onClick={handleDiscover}
          disabled={discovering || batchImporting || !canDiscover()}
        >
          <MagnifyingGlassIcon />
          {discovering ? 'Discovering...' : 'Discover Clusters'}
        </button>
        {discoveredClusters.length > 0 && (
          <button
            className="cloud-btn cloud-btn-secondary"
            onClick={handleBatchImport}
            disabled={batchImporting || discoveredClusters.every(c => c.isImported)}
          >
            <DownloadIcon />
            {batchImporting
              ? `Importing${batchProgress ? ` (${batchProgress.current}/${batchProgress.total})` : '...'}`
              : `Import All (${discoveredClusters.filter(c => !c.isImported).length})`}
          </button>
        )}
      </div>

      {(discovering || batchImporting) && (
        <div className="cloud-loading-inline">
          <div className="cloud-spinner"></div>
          <span>
            {batchImporting
              ? 'Importing clusters...'
              : discoveryProgress
                ? discoveryProgress.status && discoveryProgress.regionsScanned === 0
                  ? discoveryProgress.status
                  : `Scanning regions (${discoveryProgress.regionsScanned}/${discoveryProgress.totalRegions}) - ${discoveryProgress.clustersFound} clusters found`
                : 'Starting discovery...'}
          </span>
        </div>
      )}

      <div className="cloud-clusters-list">
        {discoveredClusters.length === 0 && !discovering ? (
          <div className="cloud-clusters-empty">
            <CubeIcon />
            <p>No clusters discovered yet</p>
            <span>Select accounts and click Discover</span>
          </div>
        ) : (
          discoveredClusters.map(cluster => (
            <div key={cluster.id} className={`cloud-cluster-item ${cluster.accessChecked && !cluster.hasAccess ? 'no-access' : ''}`}>
              {renderProviderIcon(cluster.provider)}
              <div className="cloud-cluster-info">
                <div className="cloud-cluster-name">
                  {cluster.name}
                  {!cluster.accessChecked && (
                    <span className="cloud-checking-access-badge">
                      <span className="cloud-mini-spinner" /> Checking...
                    </span>
                  )}
                  {cluster.accessChecked && !cluster.hasAccess && (
                    <span className="cloud-no-access-badge" title={cluster.accessError || 'No access'}>
                      <ExclamationTriangleIcon /> No access
                    </span>
                  )}
                </div>
                <div className="cloud-cluster-meta">
                  <span className="cloud-cluster-meta-item">
                    <DrawingPinIcon /> {cluster.region}
                  </span>
                  {cluster.version && (
                    <span className="cloud-cluster-meta-item">
                      <BookmarkIcon /> v{cluster.version}
                    </span>
                  )}
                  {cluster.accountId && (
                    <span className="cloud-cluster-account">{cluster.accountId}</span>
                  )}
                </div>
              </div>
              {cluster.availableRoles && cluster.availableRoles.length > 0 && !cluster.isImported && (
                <select
                  className="cloud-role-select"
                  value={selectedRoles.get(cluster.id) || cluster.availableRoles[0]}
                  onChange={(e) => setSelectedRoles(prev => new Map(prev).set(cluster.id, e.target.value))}
                  disabled={importing === cluster.id || cluster.availableRoles.length === 1}
                >
                  {cluster.availableRoles.map(role => (
                    <option key={role} value={role}>{role}</option>
                  ))}
                </select>
              )}
              <span className={`cloud-cluster-status status-${cluster.status.toLowerCase()}`}>
                <span className="cloud-cluster-status-dot"></span>
                {cluster.status}
              </span>
              <button
                className={`cloud-import-btn ${cluster.isImported ? 'imported' : ''}`}
                onClick={() => handleImport(cluster)}
                disabled={cluster.isImported || importing === cluster.id}
              >
                {importing === cluster.id ? 'Importing...' : cluster.isImported ? <><CheckIcon /> Imported</> : <><DownloadIcon /> Import</>}
              </button>
            </div>
          ))
        )}
      </div>
    </div>
  );

  return (
    <div className="cloud-discovery">
      <div className="cloud-tabs">
        <button className={`cloud-tab ${activeTab === 'aws' ? 'active' : ''}`} onClick={() => setActiveTab('aws')}>
          <AWSIcon /> AWS
        </button>
        <button className="cloud-tab disabled" disabled title="Coming soon">
          <GCPIcon /> GCP
        </button>
        <button className="cloud-tab disabled" disabled title="Coming soon">
          <AzureIcon /> Azure
        </button>
      </div>

      <div className="cloud-split-layout">
        <div className="cloud-left-panel">
          {activeTab === 'aws' && renderAWSSettings()}
          {activeTab === 'gcp' && renderGCPSettings()}
          {activeTab === 'azure' && renderAzureSettings()}
        </div>
        <div className="cloud-right-panel">
          {renderClustersPanel()}
        </div>
      </div>
    </div>
  );
};
