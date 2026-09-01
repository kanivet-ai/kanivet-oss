import { useState, useEffect, useRef } from 'react';
import { CheckCircledIcon, RocketIcon, Cross2Icon, DownloadIcon, UpdateIcon, CrossCircledIcon } from '@radix-ui/react-icons';
import './UpdateBanner.css';

interface UpdateInfo {
  version: string;
  releaseNotes?: string;
}

type UpdateState = 'checking' | 'available' | 'downloading' | 'ready' | 'up-to-date' | 'error' | 'stuck' | 'dismissed';

const UpdateBanner = () => {
  const [updateState, setUpdateState] = useState<UpdateState | null>(null);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [downloadProgress, setDownloadProgress] = useState<number>(0);
  const [currentVersion, setCurrentVersion] = useState<string>('');
  const [errorMessage, setErrorMessage] = useState<string>('');
  const upToDateTimerRef = useRef<NodeJS.Timeout | null>(null);
  const consecutiveErrorsRef = useRef<number>(0);
  const updateStateRef = useRef<UpdateState | null>(null);

  const electronAPI = (window as any).electronAPI;
  const hasUpdater = !!electronAPI?.updater;

  const setBannerState = (state: UpdateState | null) => {
    updateStateRef.current = state;
    setUpdateState(state);
  };

  useEffect(() => {
    updateStateRef.current = updateState;
  }, [updateState]);

  const handleCheckForUpdates = async () => {
    if (!hasUpdater) return;
    if (updateStateRef.current === 'downloading' || updateStateRef.current === 'ready') return;
    setBannerState('checking');
    setErrorMessage('');
    try {
      const result = await electronAPI.updater.checkForUpdates();
      if (result.error) {
        setErrorMessage(result.error.includes('timed out') ? 'Check timed out' : 'Check failed');
        setBannerState('error');
        upToDateTimerRef.current = setTimeout(() => setBannerState(null), 4000);
      } else if (result.checking) {
        setBannerState('checking');
      } else if (result.updateAvailable) {
        setUpdateInfo({ version: result.version, releaseNotes: result.releaseNotes });
        if (result.downloaded) {
          setBannerState('ready');
        } else if (result.downloading) {
          setBannerState('downloading');
        } else {
          setBannerState('available');
        }
      } else {
        setBannerState('up-to-date');
        upToDateTimerRef.current = setTimeout(() => setBannerState(null), 3000);
      }
    } catch (error) {
      console.error('[UpdateBanner] Check failed:', error);
      setErrorMessage('Check failed');
      setBannerState('error');
      upToDateTimerRef.current = setTimeout(() => setBannerState(null), 4000);
    }
  };

  useEffect(() => {
    if (!hasUpdater) return;

    electronAPI.updater.getAppVersion().then((v: string) => setCurrentVersion(v));

    const cleanupMenu = electronAPI.updater.onMenuCheckForUpdates?.(() => {
      handleCheckForUpdates();
    });

    electronAPI.updater.onUpdateAvailable((info: UpdateInfo) => {
      console.log('[UpdateBanner] Update available:', info.version);
      if (updateStateRef.current === 'downloading' || updateStateRef.current === 'ready') return;
      if (upToDateTimerRef.current) clearTimeout(upToDateTimerRef.current);
      setUpdateInfo(info);
      setBannerState('available');
    });

    electronAPI.updater.onDownloadProgress((progress: { percent: number }) => {
      setDownloadProgress(progress.percent);
    });

    electronAPI.updater.onUpdateDownloaded((info: UpdateInfo) => {
      console.log('[UpdateBanner] Update downloaded:', info.version);
      setUpdateInfo(info);
      setBannerState('ready');
    });

    electronAPI.updater.onError((error: string) => {
      console.error('[UpdateBanner] Update error:', error);
      consecutiveErrorsRef.current += 1;
      const isSha = /sha512|integrity|checksum/i.test(error || '');
      if (consecutiveErrorsRef.current >= 2 || isSha) {
        setErrorMessage(error || 'Update failed repeatedly');
        setBannerState('stuck');
      } else if (updateStateRef.current !== 'ready') {
        setBannerState('available');
      }
    });

    return () => {
      if (upToDateTimerRef.current) clearTimeout(upToDateTimerRef.current);
      cleanupMenu?.();
      electronAPI.updater.removeAllListeners();
    };
  }, [hasUpdater]);

  const handleDownload = async () => {
    if (updateStateRef.current === 'downloading' || updateStateRef.current === 'ready') return;
    try {
      consecutiveErrorsRef.current = 0;
      setDownloadProgress(0);
      setBannerState('downloading');
      await electronAPI.updater.downloadUpdate();
    } catch (error) {
      console.error('[UpdateBanner] Download failed:', error);
      setBannerState('available');
    }
  };

  const handleOpenDownloadPage = () => {
    window.open('https://releases.kanivet.io', '_blank');
  };

  const handleInstall = () => {
    electronAPI.updater.installUpdate();
  };

  const handleDismiss = () => {
    setBannerState('dismissed');
  };

  if (!updateState || updateState === 'dismissed') return null;
  if (!updateInfo && updateState !== 'checking' && updateState !== 'up-to-date' && updateState !== 'error') return null;

  return (
    <div className="update-toast">
      <button className="update-toast-close" onClick={handleDismiss} title="Dismiss">
        <Cross2Icon width={14} height={14} />
      </button>
      
      <div className="update-toast-header">
        <div className="update-toast-icon">
          {updateState === 'checking' && <UpdateIcon width={18} height={18} className="update-toast-spinner" />}
          {updateState === 'up-to-date' && <CheckCircledIcon width={18} height={18} />}
          {updateState === 'ready' && <CheckCircledIcon width={18} height={18} />}
          {updateState === 'downloading' && <DownloadIcon width={18} height={18} />}
          {updateState === 'available' && <RocketIcon width={18} height={18} />}
          {updateState === 'error' && <CrossCircledIcon width={18} height={18} />}
        </div>
        <div className="update-toast-title">
          {updateState === 'checking' && 'Checking for Updates'}
          {updateState === 'up-to-date' && 'Up to Date'}
          {updateState === 'available' && 'Update Available'}
          {updateState === 'downloading' && 'Downloading...'}
          {updateState === 'ready' && 'Ready to Install'}
          {updateState === 'error' && 'Update Check Failed'}
          {updateState === 'stuck' && 'Auto-update stuck'}
        </div>
      </div>

      <div className="update-toast-body">
        {updateState === 'checking' && (
          <span>Looking for new versions...</span>
        )}
        {updateState === 'up-to-date' && (
          <span>Kanivet {currentVersion} is the latest version</span>
        )}
        {updateState === 'available' && updateInfo && (
          <span>Version {updateInfo.version} is available</span>
        )}
        {updateState === 'downloading' && (
          <>
            <div className="update-toast-progress">
              <div className="update-toast-progress-bar" style={{ width: `${downloadProgress}%` }} />
            </div>
            <span className="update-toast-percent">{downloadProgress.toFixed(0)}%</span>
          </>
        )}
        {updateState === 'ready' && updateInfo && (
          <span>Version {updateInfo.version} is ready</span>
        )}
        {updateState === 'error' && (
          <span>{errorMessage}</span>
        )}
        {updateState === 'stuck' && (
          <span>
            Auto-update keeps failing. Please reinstall Kanivet from the website to get the latest version.
          </span>
        )}
      </div>

      <div className="update-toast-actions">
        {updateState === 'available' && (
          <button className="update-toast-btn update-toast-btn-primary" onClick={handleDownload}>
            <DownloadIcon width={12} height={12} />
            Download
          </button>
        )}
        {updateState === 'ready' && (
          <button className="update-toast-btn update-toast-btn-primary" onClick={handleInstall}>
            <UpdateIcon width={12} height={12} />
            Restart & Install
          </button>
        )}
        {updateState === 'stuck' && (
          <button className="update-toast-btn update-toast-btn-primary" onClick={handleOpenDownloadPage}>
            <DownloadIcon width={12} height={12} />
            Open Download Page
          </button>
        )}
      </div>
    </div>
  );
};

export default UpdateBanner;
