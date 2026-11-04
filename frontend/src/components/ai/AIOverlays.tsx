import React, { useEffect } from 'react';
import { useStore } from '../../store';
import { aiApi } from '../../services/api/ai';
import { useAIStore } from '../../store/aiStore';
import { useRegisteredKeyboard } from '../../hooks/useRegisteredKeyboard';
import AISettingsDialog from './AISettingsDialog';
import ChatPanel from './ChatPanel';
import DiagnosisSheet from './DiagnosisSheet';
import { toggleAskAI } from './askToggle';

/** Mounted once in the layout: the AI & MCP sheet, the chat panel, the
 * palette's diagnosis sheet, and the sync that makes the open cluster the
 * default for MCP tools. */
const AIOverlays: React.FC = () => {
  const currentTab = useStore((s) => s.currentTab);
  const settingsOpen = useAIStore((s) => s.settingsOpen);

  useEffect(() => {
    void aiApi.setContext(currentTab || null).catch(() => {});
  }, [currentTab]);

  useRegisteredKeyboard({
    'meta+j': {
      category: 'global',
      description: 'Ask AI (chat with the current context)',
      handler: (e) => {
        e.preventDefault();
        toggleAskAI();
      },
    },
  }, 'ai');

  return (
    <>
      {settingsOpen && <AISettingsDialog />}
      <ChatPanel />
      <DiagnosisSheet />
    </>
  );
};

export default AIOverlays;
