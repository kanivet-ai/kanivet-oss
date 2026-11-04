import React, { useEffect } from 'react';
import { useStore } from '../../store';
import { aiApi } from '../../services/api/ai';
import { useAIStore } from '../../store/aiStore';
import AISettingsDialog from './AISettingsDialog';
import InvestigationPanel from './InvestigationPanel';

/** Mounted once in the layout: the AI & MCP sheet, the investigation panel,
 * and the sync that makes the open cluster the default for MCP tools. */
const AIOverlays: React.FC = () => {
  const currentTab = useStore((s) => s.currentTab);
  const settingsOpen = useAIStore((s) => s.settingsOpen);

  useEffect(() => {
    void aiApi.setContext(currentTab || null).catch(() => {});
  }, [currentTab]);

  return (
    <>
      {settingsOpen && <AISettingsDialog />}
      <InvestigationPanel />
    </>
  );
};

export default AIOverlays;
