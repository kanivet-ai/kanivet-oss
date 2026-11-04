import { useAIChatStore } from '../../store/aiChatStore';
import { currentContextChips, inspectorResourceChip } from './currentContext';

/** ⌘J: Ask AI with the current cluster and namespace (an open inspector
 * resource is offered as a suggestion, not attached), or move the diagnosis sheet
 * into the side panel, or close the panel. */
export function toggleAskAI() {
  const s = useAIChatStore.getState();
  if (s.sheetOpen) {
    s.sheetToPanel();
    return;
  }
  if (s.panelOpen) {
    s.closePanel();
    return;
  }
  s.open();
  if (!s.activeId || !s.chats[s.activeId]?.running) s.newChat(currentContextChips(), inspectorResourceChip());
}
