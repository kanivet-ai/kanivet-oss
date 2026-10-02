import { useId, useState } from 'react';
import { useStore } from '../store';
import ClusterMetricsSettings from './ClusterMetricsSettings';
import { MonitorIcon } from './icons/kube';
import './ClusterSettings.css';

const SECTIONS = [
  { id: 'metrics', label: 'Metrics', description: 'Choose the source and display preferences for this cluster.', icon: MonitorIcon, component: ClusterMetricsSettings },
];

export default function ClusterSettings({ cluster }: { cluster: string }) {
  const alias = useStore((s) => s.clusterAliases[cluster]);
  const [sectionId, setSectionId] = useState(SECTIONS[0].id);
  const id = useId();
  const section = SECTIONS.find((entry) => entry.id === sectionId) || SECTIONS[0];
  const Content = section.component;

  return (
    <div className="cluster-settings">
      <header className="cluster-settings-header">
        <h2>Cluster settings</h2>
        <span title={cluster}>{alias || cluster.split('/').pop()}</span>
      </header>
      <div className="cluster-settings-layout">
        <nav className="cluster-settings-nav" aria-label="Cluster settings sections">
          {SECTIONS.map(({ id: key, label, icon: Icon }) => (
            <button key={key} type="button" aria-current={section.id === key ? 'page' : undefined} onClick={() => setSectionId(key)}>
              <Icon /><span>{label}</span>
            </button>
          ))}
        </nav>
        <section className="cluster-settings-content" aria-labelledby={`${id}-title`}>
          <div className="cluster-settings-section-header">
            <h3 id={`${id}-title`}>{section.label}</h3>
            <p>{section.description}</p>
          </div>
          <Content key={`${cluster}:${section.id}`} cluster={cluster} />
        </section>
      </div>
    </div>
  );
}
