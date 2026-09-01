export const TELEMETRY_ENABLED_STORAGE_KEY = 'kanivet.telemetryEnabled';
const LEGACY_ENABLED_STORAGE_KEY = ['kanivet.sen', 'tryLogsEnabled'].join('');

export const loadTelemetryEnabled = () => {
  try {
    const current = localStorage.getItem(TELEMETRY_ENABLED_STORAGE_KEY);
    if (current !== null) {
      localStorage.removeItem(LEGACY_ENABLED_STORAGE_KEY);
      return current !== 'false';
    }
    const enabled = localStorage.getItem(LEGACY_ENABLED_STORAGE_KEY) !== 'false';
    localStorage.setItem(TELEMETRY_ENABLED_STORAGE_KEY, String(enabled));
    localStorage.removeItem(LEGACY_ENABLED_STORAGE_KEY);
    return enabled;
  } catch {
    return true;
  }
};

export const persistTelemetryEnabled = (enabled: boolean) => {
  try {
    localStorage.setItem(TELEMETRY_ENABLED_STORAGE_KEY, String(enabled));
    localStorage.removeItem(LEGACY_ENABLED_STORAGE_KEY);
  } catch {}
  return enabled;
};
