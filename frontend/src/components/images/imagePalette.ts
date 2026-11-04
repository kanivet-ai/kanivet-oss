/** Palette support for "> inspect image <ref>". */

export const IMAGE_REF_KIND = 'ImageRef';

const PREFIX = /^>\s*inspect\s+image\S*\s*/i;

/** The reference typed after "> inspect image", or null when the query is
 * not that command. An empty string means the command with no ref yet. */
export function imageRefFromQuery(query: string): string | null {
  const q = query.trimStart();
  if (!PREFIX.test(q)) return null;
  return q.replace(PREFIX, '').trim();
}

/** Image references found in list items already loaded in this window. */
export function knownImages(cache: Map<string, any> | undefined = (window as any).__kanivetItemsCache): string[] {
  const out = new Set<string>();
  if (!cache) return [];
  const add = (img: unknown) => {
    if (typeof img === 'string' && img) out.add(img);
  };
  for (const topic of cache.values()) {
    const items = topic instanceof Map ? topic.values() : Array.isArray(topic) ? topic : [];
    for (const item of items as Iterable<any>) {
      const spec = item?.spec?.template?.spec || item?.spec;
      for (const c of [...(spec?.containers || []), ...(spec?.initContainers || [])]) add(c?.image);
      if (Array.isArray(item?.images)) item.images.forEach(add);
      if (out.size > 2000) return Array.from(out);
    }
  }
  return Array.from(out).sort();
}

export function imagePaletteResults(ref: string, cluster: string | null, known: string[] = knownImages()) {
  const q = ref.toLowerCase();
  const matches = known.filter((i) => !q || i.toLowerCase().includes(q)).slice(0, 20);
  const refs = ref && !matches.includes(ref) ? [ref, ...matches] : matches;
  return refs.map((r) => ({
    resource: {
      id: `image:${r}`,
      name: r,
      kind: IMAGE_REF_KIND,
      cluster: cluster || '',
      category: r === ref ? 'Inspect image' : 'Images in use',
    },
  }));
}
