/**
 * The sidebar's source-list sections. The tree stays a flat list of root
 * nodes (so lookups and count updates keep working on `treeData`); each root
 * carries a `section` and the sidebar groups them under headers.
 */

export type SidebarSectionId = 'top' | 'resources' | 'insights' | 'cost' | 'integrations' | 'bottom';

export const SIDEBAR_SECTIONS: { id: SidebarSectionId; label: string | null }[] = [
  { id: 'top', label: null },
  { id: 'resources', label: 'Resources' },
  { id: 'insights', label: 'Insights' },
  { id: 'cost', label: 'Cost' },
  { id: 'integrations', label: 'Integrations' },
  { id: 'bottom', label: null },
];

/** Node types that open a page tab rather than expanding. */
export const PAGE_NODE_TYPES = new Set([
  'overview', 'argo-overview', 'gitops', 'cnpg-overview', 'helm', 'finops', 'rightsizing',
  'certificates', 'topology', 'network', 'cluster-settings', 'incident-timeline', 'issues',
  'audit', 'changes', 'access', 'upgrade-readiness',
]);

export interface SidebarGroup<N> {
  id: SidebarSectionId;
  label: string | null;
  nodes: N[];
  /** Integrations that are not installed: shown only inside a fold. */
  notDetected: N[];
}

type SectionedNode = { section?: string; disabled?: boolean };

/** Groups root nodes by section, in section order; empty sections drop out. */
export function groupSidebar<N extends SectionedNode>(roots: N[]): SidebarGroup<N>[] {
  return SIDEBAR_SECTIONS.map(({ id, label }) => {
    const members = roots.filter((n) => (n.section || 'resources') === id);
    const fold = id === 'integrations';
    return {
      id,
      label,
      nodes: fold ? members.filter((n) => !n.disabled) : members,
      notDetected: fold ? members.filter((n) => n.disabled) : [],
    };
  }).filter((g) => g.nodes.length > 0 || g.notDetected.length > 0);
}

export const NOT_DETECTED_KEY = 'integrations:not-detected';
const STORAGE_KEY = 'kanivet.sidebar.collapsedSections';

export function loadCollapsedSections(): Set<string> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw === null) return new Set([NOT_DETECTED_KEY]);
    const parsed = JSON.parse(raw);
    return new Set(Array.isArray(parsed) ? parsed.filter((v) => typeof v === 'string') : []);
  } catch {
    return new Set([NOT_DETECTED_KEY]);
  }
}

export function saveCollapsedSections(collapsed: Set<string>) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify([...collapsed]));
  } catch {
    // Storage unavailable: the state lasts for this session only.
  }
}

/**
 * The root nodes the keyboard can reach, in display order: members of a
 * collapsed section (or a closed "Not detected" fold) are skipped. A search
 * shows every section.
 */
export function visibleRoots<N extends SectionedNode>(roots: N[], collapsed: Set<string>, searching: boolean): N[] {
  const out: N[] = [];
  for (const g of groupSidebar(roots)) {
    if (!searching && g.label && collapsed.has(g.id)) continue;
    out.push(...g.nodes);
    if (searching || !collapsed.has(NOT_DETECTED_KEY)) out.push(...g.notDetected);
  }
  return out;
}
