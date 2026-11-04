import { useAIChatStore } from '../../store/aiChatStore';
import { currentContextChips } from './currentContext';

/** ⌘J: Ask AI with the current tab's context, or move the diagnosis sheet
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
  if (!s.activeId || !s.chats[s.activeId]?.running) s.newChat(currentContextChips());
}
