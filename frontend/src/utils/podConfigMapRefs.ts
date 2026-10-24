// Collects every ConfigMap a pod spec references, with how each one is
// consumed, so the pod detail view can show the values next to the usage.

export interface ConfigMapUsage {
  container?: string;
  text: string;
}

export interface PodConfigMapRef {
  name: string;
  optional: boolean;
  usages: ConfigMapUsage[];
  // Keys the pod reads by name (env keyRefs, volume items). A key listed here
  // that the ConfigMap lacks is worth flagging.
  referencedKeys: string[];
}

export function collectPodConfigMapRefs(spec: any): PodConfigMapRef[] {
  const refs = new Map<string, PodConfigMapRef>();
  const ref = (name: string, optional?: boolean) => {
    let r = refs.get(name);
    if (!r) {
      r = { name, optional: true, usages: [], referencedKeys: [] };
      refs.set(name, r);
    }
    // Required by any consumer means the pod can't start without it.
    if (!optional) r.optional = false;
    return r;
  };
  const addKey = (r: PodConfigMapRef, key: string) => {
    if (!r.referencedKeys.includes(key)) r.referencedKeys.push(key);
  };

  const containers: any[] = [
    ...(spec?.initContainers || []),
    ...(spec?.containers || []),
    ...(spec?.ephemeralContainers || []),
  ];

  const volumeSources = (vol: any): any[] => {
    const sources: any[] = [];
    if (vol.configMap?.name) sources.push(vol.configMap);
    (vol.projected?.sources || []).forEach((s: any) => { if (s.configMap?.name) sources.push(s.configMap); });
    return sources;
  };

  (spec?.volumes || []).forEach((vol: any) => {
    volumeSources(vol).forEach((src: any) => {
      const r = ref(src.name, src.optional);
      (src.items || []).forEach((item: any) => addKey(r, item.key));
      const mounts = containers.flatMap((c) =>
        (c.volumeMounts || []).filter((m: any) => m.name === vol.name).map((m: any) => ({ container: c.name, mount: m })),
      );
      if (mounts.length === 0) {
        r.usages.push({ text: `volume ${vol.name} (not mounted)` });
      }
      mounts.forEach(({ container, mount }) => {
        const path = mount.subPath ? `${mount.mountPath} (subPath ${mount.subPath})` : mount.mountPath;
        r.usages.push({ container, text: `mounted at ${path}` });
      });
    });
  });

  containers.forEach((c) => {
    (c.envFrom || []).forEach((ef: any) => {
      if (!ef.configMapRef?.name) return;
      const r = ref(ef.configMapRef.name, ef.configMapRef.optional);
      r.usages.push({ container: c.name, text: ef.prefix ? `all keys as env (prefix ${ef.prefix})` : 'all keys as env' });
    });
    (c.env || []).forEach((env: any) => {
      const keyRef = env.valueFrom?.configMapKeyRef;
      if (!keyRef?.name) return;
      const r = ref(keyRef.name, keyRef.optional);
      addKey(r, keyRef.key);
      r.usages.push({ container: c.name, text: `$${env.name} ← ${keyRef.key}` });
    });
  });

  return [...refs.values()].sort((a, b) => a.name.localeCompare(b.name));
}
