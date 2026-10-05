/**
 * What a saved tab snapshot keeps of an object a tab shows: enough to find
 * and load it again. A detail tab holds the full object, a Secret's data
 * included, and a list row of an earlier release kubectl's last-applied copy
 * of it; localStorage is a file on disk, so neither may be saved whole.
 */
export const persistedItem = (item: any) => {
  if (!item || typeof item !== 'object') return item;
  const meta = item.metadata || {};
  return {
    name: meta.name ?? item.name,
    namespace: meta.namespace ?? item.namespace,
    uid: meta.uid ?? item.uid,
    kind: item.kind,
    apiVersion: item.apiVersion,
  };
};
