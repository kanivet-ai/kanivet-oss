export const STAR_PROJECT_PROMPT_DISMISSED_KEY =
  'kanivet.starProjectPrompt.dismissed';

const getStorage = (): Storage | null => {
  try {
    return typeof window === 'undefined' ? null : window.localStorage;
  } catch {
    return null;
  }
};

export const isStarProjectPromptDismissed = (): boolean => {
  try {
    return getStorage()?.getItem(STAR_PROJECT_PROMPT_DISMISSED_KEY) === 'true';
  } catch {
    return false;
  }
};

export const setStarProjectPromptDismissed = (dismissed: boolean): void => {
  try {
    const storage = getStorage();
    if (!storage) return;

    if (dismissed) {
      storage.setItem(STAR_PROJECT_PROMPT_DISMISSED_KEY, 'true');
    } else {
      storage.removeItem(STAR_PROJECT_PROMPT_DISMISSED_KEY);
    }
  } catch {
    // Storage can be unavailable in private browsing or restricted contexts.
  }
};
