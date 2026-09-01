import { useCallback } from 'react';
import { HelmRelease } from '../types/helm';
import HelmReleaseList from './HelmReleaseList';
import { useStore } from '../store';

interface HelmPageProps {
  cluster: string;
}

const HelmPage = ({ cluster }: HelmPageProps) => {
  const { openDetailTab } = useStore();

  const handleSelectRelease = useCallback((release: HelmRelease) => {
    const helmItem = {
      ...release,
      kind: 'HelmRelease',
      apiVersion: 'helm.sh/v1',
    };
    openDetailTab({ kind: 'HelmRelease' }, helmItem, cluster);
  }, [cluster, openDetailTab]);

  return (
    <HelmReleaseList
      cluster={cluster}
      onSelectRelease={handleSelectRelease}
    />
  );
};

export default HelmPage;

