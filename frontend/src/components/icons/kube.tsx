import type { ReactNode, SVGProps } from 'react';

/**
 * Kanivet's Kubernetes icon pack.
 *
 * Every glyph is drawn on a 16×16 grid with a 1.25 monoline stroke, round caps and joins, and
 * `currentColor` so the cascade sets the color. Rendered at the app's usual 14px this lands at
 * ~1.1px, which sits next to the Radix chrome icons without looking heavier.
 *
 * The hexagon is the pod. Every workload kind is a hexagon plus one qualifier so the family reads
 * as a family; everything else gets a purpose-built glyph. Cluster-scoped variants of a namespaced
 * kind reuse the base glyph and add a small ring in the top-right corner.
 */

export interface KubeIconProps
  extends Omit<SVGProps<SVGSVGElement>, 'width' | 'height'> {
  width?: number | string;
  height?: number | string;
}

const Svg = ({
  width = 15,
  height = 15,
  children,
  ...rest
}: KubeIconProps & { children: ReactNode }) => (
  <svg
    width={width}
    height={height}
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth={1.25}
    strokeLinecap="round"
    strokeLinejoin="round"
    xmlns="http://www.w3.org/2000/svg"
    aria-hidden="true"
    focusable="false"
    {...rest}
  >
    {children}
  </svg>
);

/** Pointy-top hexagon path centred on (cx, cy) with circumradius r. */
const hex = (cx: number, cy: number, r: number): string => {
  const w = r * 0.866;
  const h = r * 0.5;
  const f = (n: number) => +n.toFixed(2);
  return (
    `M${f(cx)} ${f(cy - r)}` +
    `L${f(cx + w)} ${f(cy - h)}` +
    `L${f(cx + w)} ${f(cy + h)}` +
    `L${f(cx)} ${f(cy + r)}` +
    `L${f(cx - w)} ${f(cy + h)}` +
    `L${f(cx - w)} ${f(cy - h)}Z`
  );
};

const HEX_FULL = hex(8, 8, 6.25);
const HEX_MID = hex(8, 8, 3.75);

/** Small ring marking a cluster-scoped kind. */
const ClusterScopeRing = () => <circle cx={13.25} cy={2.75} r={1.75} />;

const Dot = ({ cx, cy, r = 1 }: { cx: number; cy: number; r?: number }) => (
  <circle cx={cx} cy={cy} r={r} fill="currentColor" stroke="none" />
);

// ---------------------------------------------------------------------------
// Workloads — the hexagon family
// ---------------------------------------------------------------------------

export const PodIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_FULL} />
  </Svg>
);

/** Hexagon with a rollout arrow. */
export const DeploymentIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_FULL} />
    <path d="M8 11.25V5.25M5.75 7.5 8 5.25l2.25 2.25" />
  </Svg>
);

/** Two hexagons, one behind the other. */
export const ReplicaSetIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M5.64 5.35V3.88L9.75 1.5l4.11 2.38v4.75l-3.5 2.02" />
    <path d={hex(6.25, 9.75, 4.75)} />
  </Svg>
);

/** Hexagon in ordered tiers: stable identity, stacked state. */
export const StatefulSetIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_FULL} />
    <path d="M4.5 6.25h7M4.5 9.75h7" />
  </Svg>
);

/** One hexagon in every slot: a pod on every node. */
export const DaemonSetIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={hex(5, 4.75, 2.7)} />
    <path d={hex(11, 4.75, 2.7)} />
    <path d={hex(5, 11.25, 2.7)} />
    <path d={hex(11, 11.25, 2.7)} />
  </Svg>
);

/** Hexagon that ran to completion. */
export const JobIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_FULL} />
    <path d="M5.25 8.25l2 2 3.5-4.25" />
  </Svg>
);

/** Clock with a recurrence arrow. */
export const CronJobIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M13.25 8.5A5.25 5.25 0 1 1 10.63 3.95" />
    <path d="M10.11 2.02l.52 1.93-1.93.52" />
    <path d="M8 5.75V8.5l2 1.5" />
  </Svg>
);

/** Hexagon scaling out sideways. */
export const HorizontalPodAutoscalerIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_MID} />
    <path d="M3.5 8H1.5M2.6 6.9 1.5 8l1.1 1.1M12.5 8h2M13.4 6.9 14.5 8l-1.1 1.1" />
  </Svg>
);

/** Hexagon scaling up and down. */
export const VerticalPodAutoscalerIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_MID} />
    <path d="M8 3.5V1.5M6.9 2.6 8 1.5l1.1 1.1M8 12.5v2M6.9 13.4 8 14.5l1.1-1.1" />
  </Svg>
);

/** Umbrella: shelter from disruption. */
export const PodDisruptionBudgetIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2 8.75a6 6 0 0 1 12 0Z" />
    <path d="M8 2.75V1.75M8 8.75v3.5a1.5 1.5 0 0 0 3 0" />
  </Svg>
);

// ---------------------------------------------------------------------------
// Networking
// ---------------------------------------------------------------------------

/** One virtual IP fanning out to its endpoints. */
export const ServiceIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={4.5} cy={8} r={2.25} />
    <path d="M6.75 8l4.75-4.25M6.75 8h4.75M6.75 8l4.75 4.25" />
    <Dot cx={12.5} cy={3.75} r={1.1} />
    <Dot cx={12.5} cy={8} r={1.1} />
    <Dot cx={12.5} cy={12.25} r={1.1} />
  </Svg>
);

/** Traffic entering through the gate. */
export const IngressIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M9 2.5h3.5A1.5 1.5 0 0 1 14 4v8a1.5 1.5 0 0 1-1.5 1.5H9" />
    <path d="M1.5 8h8M7 5.5 9.5 8 7 10.5" />
  </Svg>
);

export const EndpointsIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={8} cy={8} r={5.5} />
    <Dot cx={8} cy={8} r={1.75} />
  </Svg>
);

export const EndpointSliceIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={8} cy={8} r={5.5} />
    <path d="M8 8V2.5M8 8h5.5" />
  </Svg>
);

/** Shield with a rule across it. */
export const NetworkPolicyIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M8 1.75 13.25 3.75V8c0 3.25-2.25 5.5-5.25 6.5C5 13.5 2.75 11.25 2.75 8V3.75Z" />
    <path d="M5.75 8h4.5" />
  </Svg>
);

// ---------------------------------------------------------------------------
// Config & policy
// ---------------------------------------------------------------------------

/** Card of key–value rows. */
export const ConfigMapIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={2.5} y={2.5} width={11} height={11} rx={2} />
    <rect
      x={5}
      y={5.25}
      width={1.75}
      height={1.75}
      rx={0.4}
      fill="currentColor"
      stroke="none"
    />
    <rect
      x={5}
      y={9}
      width={1.75}
      height={1.75}
      rx={0.4}
      fill="currentColor"
      stroke="none"
    />
    <path d="M8.5 6.13H11M8.5 9.88H11" />
  </Svg>
);

/** A key. */
export const SecretIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={5.5} cy={5.5} r={2.75} />
    <path d="M7.45 7.45l5.8 5.8M10.75 10.75l1.5-1.5M12.5 12.5 14 11" />
  </Svg>
);

/** Gauge. */
export const ResourceQuotaIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2.5 11a5.5 5.5 0 0 1 11 0Z" />
    <path d="M8 11l2.75-4.5" />
    <Dot cx={8} cy={11} r={1} />
  </Svg>
);

/** Range slider with two stops. */
export const LimitRangeIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2 8h12M2 6.25v3.5M14 6.25v3.5" />
    <Dot cx={5.75} cy={8} r={1.5} />
    <Dot cx={10.25} cy={8} r={1.5} />
  </Svg>
);

export const PriorityClassIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M4 8.5 8 4.5l4 4M4 12.5l4-4 4 4" />
  </Svg>
);

/** Corner brackets bounding a pod: a named space. */
export const NamespaceIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2.5 6V2.5H6M10 2.5h3.5V6M13.5 10v3.5H10M6 13.5H2.5V10" />
    <path d={hex(8, 8, 2.75)} />
  </Svg>
);

/** Rack unit. */
export const NodeIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={2} y={3} width={12} height={10} rx={1.75} />
    <path d="M2 8h12M7.5 5.5h4M7.5 10.5h4" />
    <Dot cx={4.75} cy={5.5} r={0.9} />
    <Dot cx={4.75} cy={10.5} r={0.9} />
  </Svg>
);

export const EventIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M9 1.75 4 9h4l-1 5.25L12 7H8Z" />
  </Svg>
);

// ---------------------------------------------------------------------------
// Storage
// ---------------------------------------------------------------------------

export const PersistentVolumeIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <ellipse cx={8} cy={4} rx={5.5} ry={2} />
    <path d="M2.5 4v8a5.5 2 0 0 0 11 0V4" />
  </Svg>
);

/** A ticket: the claim on a volume. */
export const PersistentVolumeClaimIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M3 4.5h10a1 1 0 0 1 1 1v1.25a1.25 1.25 0 0 0 0 2.5v1.25a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V9.25a1.25 1.25 0 0 0 0-2.5V5.5a1 1 0 0 1 1-1Z" />
    <path d="M10 6.25v3.5" strokeDasharray="1 1.5" />
  </Svg>
);

/** Tiers. */
export const StorageClassIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M8 2.5 13.5 5.5 8 8.5 2.5 5.5Z" />
    <path d="M2.5 8.25 8 11.25l5.5-3M2.5 11 8 14l5.5-3" />
  </Svg>
);

// ---------------------------------------------------------------------------
// RBAC
// ---------------------------------------------------------------------------

/** A machine identity. */
export const ServiceAccountIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={3} y={5.5} width={10} height={8} rx={2} />
    <path d="M8 5.5V3.5" />
    <Dot cx={8} cy={2.5} r={0.9} />
    <Dot cx={6} cy={9.5} r={1} />
    <Dot cx={10} cy={9.5} r={1} />
  </Svg>
);

/** ID badge. */
export const RoleIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={3.5} y={3.25} width={9} height={11} rx={1.5} />
    <path d="M6.5 3.25V2.5a.75.75 0 0 1 .75-.75h1.5a.75.75 0 0 1 .75.75v.75" />
    <circle cx={8} cy={7.25} r={1.75} />
    <path d="M5.75 11.25h4.5" />
  </Svg>
);

export const ClusterRoleIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={2.5} y={3.75} width={8.5} height={10.5} rx={1.5} />
    <path d="M5.5 3.75V3a.75.75 0 0 1 .75-.75h1a.75.75 0 0 1 .75.75v.75" />
    <circle cx={6.75} cy={7.75} r={1.75} />
    <path d="M4.5 11.75h4.5" />
    <ClusterScopeRing />
  </Svg>
);

/** Two interlocking links. */
export const RoleBindingIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={1.5} y={4.25} width={7.5} height={5} rx={2.5} />
    <rect x={7} y={6.75} width={7.5} height={5} rx={2.5} />
  </Svg>
);

export const ClusterRoleBindingIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={1.5} y={5.5} width={7} height={5} rx={2.5} />
    <rect x={6.5} y={8} width={7} height={5} rx={2.5} />
    <ClusterScopeRing />
  </Svg>
);

// ---------------------------------------------------------------------------
// Extension & admission
// ---------------------------------------------------------------------------

/** Puzzle piece. */
export const CustomResourceDefinitionIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M3.25 4.75h2.5a1.5 1.5 0 0 1 3 0h2.5v2.5a1.5 1.5 0 0 1 0 3v2.5h-8Z" />
  </Svg>
);

/** Transform: in one shape, out another. */
export const MutatingWebhookIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2.5 5.5h11M11 3l2.5 2.5L11 8M13.5 10.5h-11M5 8l-2.5 2.5L5 13" />
  </Svg>
);

export const ValidatingWebhookIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={2.5} y={2.5} width={11} height={11} rx={2.5} />
    <path d="M5.25 8.25l2 2 3.5-4.25" />
  </Svg>
);

/** Rosette. */
export const CertificateIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={8} cy={6} r={4.25} />
    <path d="M5.5 9.9 4.25 14.25 8 12.5l3.75 1.75L10.5 9.9" />
  </Svg>
);

/** Stamp. */
export const IssuerIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={6.5} y={1.75} width={3} height={3} rx={1.5} />
    <path d="M8 4.75V7M2.5 11V9a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v2ZM4.5 13.75h7" />
  </Svg>
);

export const ClusterIssuerIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={5.75} y={2.25} width={3} height={3} rx={1.5} />
    <path d="M7.25 5.25V7.5M2.5 11.5V9.5a2 2 0 0 1 2-2h5.5a2 2 0 0 1 2 2v2ZM4 14.25h6.5" />
    <ClusterScopeRing />
  </Svg>
);

// ---------------------------------------------------------------------------
// Sidebar sections & fallbacks
// ---------------------------------------------------------------------------

/** Bento grid. */
export const OverviewIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={2.5} y={2.5} width={6} height={6.5} rx={1.5} />
    <rect x={10.25} y={2.5} width={3.25} height={6.5} rx={1.5} />
    <rect x={2.5} y={10.75} width={11} height={2.75} rx={1.375} />
  </Svg>
);

/** Price tag. */
export const FinOpsIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2.5 2.5h5.75l5.5 5.5L8 13.75 2.5 8.25Z" />
    <circle cx={5.5} cy={5.5} r={1.1} />
  </Svg>
);

/** Hexagon with a live core. */
export const WorkloadsIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_FULL} />
    <Dot cx={8} cy={8} r={1.6} />
  </Svg>
);

/** Sliders. */
export const ConfigIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2.5 5.5h11M2.5 10.5h11" />
    <Dot cx={10} cy={5.5} r={1.75} />
    <Dot cx={6} cy={10.5} r={1.75} />
  </Svg>
);

/** Three connected nodes. */
export const NetworkIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M8 3 3 12.5h10Z" />
    <Dot cx={8} cy={3} r={1.6} />
    <Dot cx={3} cy={12.5} r={1.6} />
    <Dot cx={13} cy={12.5} r={1.6} />
  </Svg>
);

/** Drive. */
export const StorageIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={2} y={4.5} width={12} height={7} rx={1.75} />
    <path d="M4.5 8h4" />
    <Dot cx={11.25} cy={8} r={0.9} />
  </Svg>
);

/** Padlock. */
export const RbacIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M5 7V5a3 3 0 0 1 6 0v2" />
    <rect x={3} y={7} width={10} height={7} rx={1.5} />
    <Dot cx={8} cy={10.5} r={1} />
  </Svg>
);

/** Globe. */
export const ClusterIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={8} cy={8} r={6} />
    <ellipse cx={8} cy={8} rx={2.5} ry={6} />
    <path d="M2 8h12" />
  </Svg>
);

/** A hexagon inside a hexagon. */
export const VirtualClusterIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d={HEX_FULL} />
    <path d={hex(8, 8, 3)} />
  </Svg>
);

/** Box: an API group. */
export const PackageIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M8 1.75 14 4.75v6.5l-6 3-6-3v-6.5Z" />
    <path d="M2 4.75l6 3 6-3M8 7.75v6.5" />
  </Svg>
);

export const FolderIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M2 4.5A1 1 0 0 1 3 3.5h3.5L8 5h5a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1Z" />
  </Svg>
);

/** Generic manifest, used for kinds the pack doesn't know. */
export const ResourceIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M4 2.5h5.5L13 6v6.5a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-9a1 1 0 0 1 1-1Z" />
    <path d="M9.5 2.5V6H13" />
  </Svg>
);

// ---------------------------------------------------------------------------
// Ecosystem kinds and inferred fallbacks
// ---------------------------------------------------------------------------

/** Tiers: any `*Class` kind. */
export const LayersIcon = StorageClassIcon;

/** Archway: a gateway into the cluster. */
export const GatewayIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M3 14V7.5a5 5 0 0 1 10 0V14" />
    <path d="M1.5 14h13" />
  </Svg>
);

/** A path between two points. */
export const RouteIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M3.5 12.5C3.5 8 12.5 8 12.5 3.5" />
    <Dot cx={3.5} cy={12.5} r={1.6} />
    <Dot cx={12.5} cy={3.5} r={1.6} />
  </Svg>
);

/** Pulse line: monitoring, alerting, metrics. */
export const MonitorIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <path d="M1.75 8.5h2.75l1.75-4 2.5 7.5 1.75-4.5 1 1h2.75" />
  </Svg>
);

/** Archive box: backups, snapshots, restores. */
export const ArchiveIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={2} y={2.75} width={12} height={3} rx={1} />
    <path d="M3 5.75v6.5a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1v-6.5M6.5 9h3" />
  </Svg>
);

/** Clock: leases, schedules. */
export const ClockIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={8} cy={8} r={5.75} />
    <path d="M8 4.75V8l2.25 1.5" />
  </Svg>
);

/** Two stages joined by an arrow: pipelines, workflows, tasks. */
export const PipelineIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <rect x={1.5} y={5.5} width={4} height={5} rx={1} />
    <rect x={10.5} y={5.5} width={4} height={5} rx={1} />
    <path d="M5.5 8h4.5M8.5 6.5 10 8l-1.5 1.5" />
  </Svg>
);

/** A person: human identities, users, groups. */
export const PersonIcon = (p: KubeIconProps) => (
  <Svg {...p}>
    <circle cx={8} cy={5.25} r={2.5} />
    <path d="M2.75 14a5.25 5.25 0 0 1 10.5 0" />
  </Svg>
);
