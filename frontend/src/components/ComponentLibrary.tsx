import { useState, useRef, useCallback, useEffect } from 'react';
import { Cross2Icon } from '@radix-ui/react-icons';
import * as Tabs from '@radix-ui/react-tabs';
import StatusIndicator from './common/StatusIndicator';
import ResourceDetailSkeleton from './ResourceDetailSkeleton';
import ResourceDetailView from './detailView/ResourceDetailView';
import './ComponentLibrary.css';

interface ComponentLibraryProps {
  onClose: () => void;
}

interface ResizableContainerProps {
  children: React.ReactNode;
  className: string;
  initialWidth?: number;
}

const ResizableContainer = ({ children, className, initialWidth }: ResizableContainerProps) => {
  const [width, setWidth] = useState<number | null>(initialWidth || null);
  const containerRef = useRef<HTMLDivElement>(null);
  const isResizing = useRef(false);

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    isResizing.current = true;
  }, []);

  const handleMouseMove = useCallback((e: MouseEvent) => {
    if (!isResizing.current || !containerRef.current) return;

    const containerRect = containerRef.current.getBoundingClientRect();
    const newWidth = e.clientX - containerRect.left;

    if (newWidth >= 200 && newWidth <= 2000) {
      setWidth(newWidth);
    }
  }, []);

  const handleMouseUp = useCallback(() => {
    isResizing.current = false;
  }, []);

  useEffect(() => {
    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [handleMouseMove, handleMouseUp]);

  return (
    <div ref={containerRef} className={className} style={width ? { width: `${width}px` } : {}}>
      <div className="resize-handle-left" onMouseDown={handleMouseDown} />
      {children}
    </div>
  );
};

const dummyPodData = {
  apiVersion: 'v1',
  kind: 'Pod',
  metadata: {
    name: 'nginx-deployment-7d8b9c5f4d-x9k2p',
    namespace: 'default',
    uid: 'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
    creationTimestamp: '2024-01-15T10:30:00Z',
    labels: {
      'app': 'nginx',
      'pod-template-hash': '7d8b9c5f4d',
      'version': 'v1.0.0',
    },
    annotations: {
      'kubernetes.io/psp': 'restricted',
    },
  },
  spec: {
    nodeName: 'worker-node-1',
    serviceAccountName: 'default',
    containers: [
      {
        name: 'nginx',
        image: 'nginx:1.21.0',
        ports: [{ containerPort: 80, protocol: 'TCP' }],
        env: [
          { name: 'ENVIRONMENT', value: 'production' },
          { name: 'LOG_LEVEL', value: 'info' },
          { name: 'MAX_CONNECTIONS', value: '1000' },
          { name: 'DB_HOST', valueFrom: { configMapKeyRef: { name: 'app-config', key: 'database.host' } } },
          { name: 'DB_PASSWORD', valueFrom: { secretKeyRef: { name: 'app-secrets', key: 'database.password' } } },
        ],
        envFrom: [
          { configMapRef: { name: 'app-config' } },
          { secretRef: { name: 'app-secrets' } },
        ],
        volumeMounts: [
          { name: 'config', mountPath: '/etc/nginx/nginx.conf', subPath: 'nginx.conf', readOnly: true },
          { name: 'cache', mountPath: '/var/cache/nginx' },
          { name: 'logs', mountPath: '/var/log/nginx' },
          { name: 'tls-certs', mountPath: '/etc/nginx/ssl', readOnly: true },
        ],
        resources: {
          limits: { cpu: '500m', memory: '512Mi' },
          requests: { cpu: '250m', memory: '256Mi' },
        },
        livenessProbe: {
          httpGet: { path: '/healthz', port: 80, scheme: 'HTTP' },
          initialDelaySeconds: 30,
          periodSeconds: 10,
          timeoutSeconds: 5,
          successThreshold: 1,
          failureThreshold: 3,
        },
        readinessProbe: {
          httpGet: { path: '/ready', port: 80, scheme: 'HTTP' },
          initialDelaySeconds: 5,
          periodSeconds: 5,
          timeoutSeconds: 3,
          successThreshold: 1,
          failureThreshold: 2,
        },
        securityContext: {
          runAsNonRoot: true,
          runAsUser: 101,
          allowPrivilegeEscalation: false,
          capabilities: { drop: ['ALL'] },
          readOnlyRootFilesystem: true,
        },
      },
    ],
    volumes: [
      { name: 'config', configMap: { name: 'nginx-config', defaultMode: 420 } },
      { name: 'cache', emptyDir: { sizeLimit: '1Gi' } },
      { name: 'logs', emptyDir: {} },
      { name: 'tls-certs', secret: { secretName: 'nginx-tls', defaultMode: 384 } },
    ],
    restartPolicy: 'Always',
    terminationGracePeriodSeconds: 30,
    dnsPolicy: 'ClusterFirst',
    securityContext: {
      fsGroup: 101,
      runAsNonRoot: true,
    },
  },
  status: {
    phase: 'Running',
    podIP: '10.244.1.5',
    hostIP: '192.168.1.10',
    startTime: '2024-01-15T10:30:05Z',
    conditions: [
      {
        type: 'Initialized',
        status: 'True',
        lastTransitionTime: '2024-01-15T10:30:01Z',
      },
      {
        type: 'Ready',
        status: 'True',
        lastTransitionTime: '2024-01-15T10:30:10Z',
      },
      {
        type: 'ContainersReady',
        status: 'True',
        lastTransitionTime: '2024-01-15T10:30:10Z',
      },
      {
        type: 'PodScheduled',
        status: 'True',
        lastTransitionTime: '2024-01-15T10:30:00Z',
      },
    ],
    containerStatuses: [
      {
        name: 'nginx',
        ready: true,
        restartCount: 0,
        image: 'nginx:1.21.0',
        imageID: 'docker-pullable://nginx@sha256:abc123',
        containerID: 'docker://1a2b3c4d5e6f',
        state: {
          running: {
            startedAt: '2024-01-15T10:30:08Z',
          },
        },
      },
    ],
  },
};

const dummyDeploymentData = {
  apiVersion: 'apps/v1',
  kind: 'Deployment',
  metadata: {
    name: 'nginx-deployment',
    namespace: 'default',
    uid: 'b2c3d4e5-f6a7-8901-bcde-f12345678901',
    creationTimestamp: '2024-01-15T10:00:00Z',
    labels: {
      'app': 'nginx',
    },
  },
  spec: {
    replicas: 3,
    selector: { matchLabels: { 'app': 'nginx' } },
    template: {
      metadata: { labels: { 'app': 'nginx' } },
      spec: {
        containers: [
          {
            name: 'nginx',
            image: 'nginx:1.21.0',
            ports: [{ containerPort: 80, protocol: 'TCP' }],
            resources: { limits: { cpu: '500m', memory: '512Mi' }, requests: { cpu: '250m', memory: '256Mi' } },
          },
          {
            name: 'sidecar',
            image: 'envoyproxy/envoy:v1.25.0',
            ports: [{ containerPort: 9901, protocol: 'TCP' }],
            resources: { limits: { cpu: '200m', memory: '256Mi' }, requests: { cpu: '100m', memory: '128Mi' } },
          },
        ],
        initContainers: [
          { name: 'init-config', image: 'busybox:1.35', command: ['sh', '-c', 'echo "Initializing..."'] },
        ],
      },
    },
  },
  status: {
    replicas: 3,
    updatedReplicas: 3,
    readyReplicas: 3,
    availableReplicas: 3,
    conditions: [
      { type: 'Available', status: 'True', lastTransitionTime: '2024-01-15T10:05:00Z', reason: 'MinimumReplicasAvailable', message: 'Deployment has minimum availability.' },
      { type: 'Progressing', status: 'True', lastTransitionTime: '2024-01-15T10:00:30Z', reason: 'NewReplicaSetAvailable', message: 'ReplicaSet "nginx-deployment-7d8b9c5f4d" has successfully progressed.' },
    ],
    containerStatuses: [
      { name: 'nginx', ready: true, restartCount: 0, image: 'nginx:1.21.0', imageID: 'docker-pullable://nginx@sha256:abc123', containerID: 'docker://1a2b3c4d5e6f', state: { running: { startedAt: '2024-01-15T10:00:15Z' } } },
      { name: 'sidecar', ready: true, restartCount: 2, image: 'envoyproxy/envoy:v1.25.0', imageID: 'docker-pullable://envoyproxy/envoy@sha256:def456', containerID: 'docker://7g8h9i0j1k2l', state: { running: { startedAt: '2024-01-15T10:00:20Z' } } },
    ],
    initContainerStatuses: [
      { name: 'init-config', ready: true, restartCount: 0, image: 'busybox:1.35', imageID: 'docker-pullable://busybox@sha256:ghi789', containerID: 'docker://3m4n5o6p7q8r', state: { terminated: { exitCode: 0, reason: 'Completed', startedAt: '2024-01-15T10:00:05Z', finishedAt: '2024-01-15T10:00:10Z' } } },
    ],
  },
};

const dummyServiceData = {
  apiVersion: 'v1',
  kind: 'Service',
  metadata: {
    name: 'nginx-service',
    namespace: 'default',
    uid: 'e4f5a6b7-c8d9-0123-ef45-678901234567',
    creationTimestamp: '2024-01-15T10:00:00Z',
    labels: {
      'app': 'nginx',
    },
  },
  spec: {
    type: 'ClusterIP',
    clusterIP: '10.96.100.50',
    ports: [
      {
        name: 'http',
        protocol: 'TCP',
        port: 80,
        targetPort: 8080,
      },
      {
        name: 'https',
        protocol: 'TCP',
        port: 443,
        targetPort: 8443,
      },
    ],
    selector: {
      'app': 'nginx',
    },
  },
  status: {
    loadBalancer: {},
  },
};

const dummyConfigMapData = {
  apiVersion: 'v1',
  kind: 'ConfigMap',
  metadata: {
    name: 'app-config',
    namespace: 'default',
    uid: 'f5a6b7c8-d9e0-1234-fa56-789012345678',
    creationTimestamp: '2024-01-15T09:30:00Z',
  },
  data: {
    'database.host': 'postgres.default.svc.cluster.local',
    'database.port': '5432',
    'app.log.level': 'info',
    'feature.flags': 'enable-auth,enable-metrics',
    'config.json': '{\n  "timeout": 30,\n  "retries": 3,\n  "cache": true\n}',
  },
};

const dummySecretData = {
  apiVersion: 'v1',
  kind: 'Secret',
  metadata: {
    name: 'app-secrets',
    namespace: 'default',
    uid: 'a6b7c8d9-e0f1-2345-ab67-890123456789',
    creationTimestamp: '2024-01-15T09:30:00Z',
  },
  type: 'Opaque',
  data: {
    'database.password': '',
    'api.key': '',
    'tls.crt': '',
  },
};

const dummyEndpointsData = {
  apiVersion: 'v1',
  kind: 'Endpoints',
  metadata: {
    name: 'nginx-service',
    namespace: 'default',
    uid: 'b7c8d9e0-f1a2-3456-bc78-901234567890',
    creationTimestamp: '2024-01-15T10:00:00Z',
  },
  subsets: [
    {
      addresses: [
        { ip: '10.244.1.5', nodeName: 'worker-node-1', targetRef: { kind: 'Pod', name: 'nginx-deployment-7d8b9c5f4d-x9k2p', namespace: 'default' } },
        { ip: '10.244.2.8', nodeName: 'worker-node-2', targetRef: { kind: 'Pod', name: 'nginx-deployment-7d8b9c5f4d-y7m3q', namespace: 'default' } },
        { ip: '10.244.3.12', nodeName: 'worker-node-3', targetRef: { kind: 'Pod', name: 'nginx-deployment-7d8b9c5f4d-z5n4r', namespace: 'default' } },
      ],
      ports: [
        { name: 'http', port: 8080, protocol: 'TCP' },
        { name: 'https', port: 8443, protocol: 'TCP' },
      ],
    },
  ],
};

const dummyRoleData = {
  apiVersion: 'rbac.authorization.k8s.io/v1',
  kind: 'Role',
  metadata: {
    name: 'pod-reader',
    namespace: 'default',
    uid: 'c8d9e0f1-a2b3-4567-cd89-012345678901',
    creationTimestamp: '2024-01-15T08:00:00Z',
  },
  rules: [
    {
      apiGroups: [''],
      resources: ['pods', 'pods/log'],
      verbs: ['get', 'list', 'watch'],
    },
    {
      apiGroups: ['apps'],
      resources: ['deployments', 'replicasets'],
      verbs: ['get', 'list'],
    },
  ],
};

const dummyRoleBindingData = {
  apiVersion: 'rbac.authorization.k8s.io/v1',
  kind: 'RoleBinding',
  metadata: {
    name: 'read-pods',
    namespace: 'default',
    uid: 'd9e0f1a2-b3c4-5678-de90-123456789abc',
    creationTimestamp: '2024-01-15T08:05:00Z',
  },
  subjects: [
    {
      kind: 'User',
      name: 'jane.doe@example.com',
      apiGroup: 'rbac.authorization.k8s.io',
    },
    {
      kind: 'ServiceAccount',
      name: 'default',
      namespace: 'default',
    },
    {
      kind: 'Group',
      name: 'system:authenticated',
      apiGroup: 'rbac.authorization.k8s.io',
    },
  ],
  roleRef: {
    apiGroup: 'rbac.authorization.k8s.io',
    kind: 'Role',
    name: 'pod-reader',
  },
};

const dummyNodeData = {
  apiVersion: 'v1',
  kind: 'Node',
  metadata: {
    name: 'worker-node-1',
    uid: 'e0f1a2b3-c4d5-6789-ef01-234567890123',
    creationTimestamp: '2024-01-10T12:00:00Z',
    labels: {
      'kubernetes.io/hostname': 'worker-node-1',
      'node.kubernetes.io/instance-type': 't3.large',
      'topology.kubernetes.io/zone': 'us-east-1a',
      'node-role.kubernetes.io/worker': '',
    },
  },
  spec: {
    podCIDR: '10.244.1.0/24',
    providerID: 'aws:///us-east-1a/i-0abc123def456789',
  },
  status: {
    capacity: {
      cpu: '2',
      memory: '8Gi',
      pods: '110',
      'ephemeral-storage': '100Gi',
    },
    allocatable: {
      cpu: '1900m',
      memory: '7.5Gi',
      pods: '110',
      'ephemeral-storage': '95Gi',
    },
    conditions: [
      {
        type: 'Ready',
        status: 'True',
        lastTransitionTime: '2024-01-10T12:05:00Z',
        reason: 'KubeletReady',
        message: 'kubelet is posting ready status',
      },
      {
        type: 'MemoryPressure',
        status: 'False',
        lastTransitionTime: '2024-01-10T12:00:00Z',
        reason: 'KubeletHasSufficientMemory',
        message: 'kubelet has sufficient memory available',
      },
      {
        type: 'DiskPressure',
        status: 'False',
        lastTransitionTime: '2024-01-10T12:00:00Z',
        reason: 'KubeletHasNoDiskPressure',
        message: 'kubelet has no disk pressure',
      },
    ],
    addresses: [
      { type: 'InternalIP', address: '192.168.1.10' },
      { type: 'ExternalIP', address: '54.123.45.67' },
      { type: 'Hostname', address: 'worker-node-1' },
    ],
    nodeInfo: {
      machineID: 'ec2abc123def456789',
      systemUUID: 'ec2abc123-def4-5678-9abc-123def456789',
      bootID: 'boot-id-123',
      kernelVersion: '5.10.0-1057-aws',
      osImage: 'Ubuntu 20.04.5 LTS',
      containerRuntimeVersion: 'containerd://1.6.8',
      kubeletVersion: 'v1.27.3',
      kubeProxyVersion: 'v1.27.3',
      operatingSystem: 'linux',
      architecture: 'amd64',
    },
  },
};

const dummyCustomResourceData = {
  apiVersion: 'crossplane.io/v1',
  kind: 'CompositePostgreSQLInstance',
  metadata: {
    name: 'production-db',
    namespace: 'default',
    uid: 'c3d4e5f6-a7b8-9012-cdef-abcdef123456',
    creationTimestamp: '2024-01-15T09:00:00Z',
    labels: {
      'crossplane.io/claim-name': 'production-db',
      'crossplane.io/claim-namespace': 'default',
      'crossplane.io/composite': 'production-db',
      'environment': 'production',
      'team': 'platform',
    },
    annotations: {
      'crossplane.io/composition-resource-name': 'database',
      'crossplane.io/external-name': 'prod-postgres-rds-abc123',
    },
    ownerReferences: [
      {
        apiVersion: 'database.example.io/v1alpha1',
        kind: 'PostgreSQLInstance',
        name: 'production-db',
        uid: 'd4e5f6a7-b8c9-0123-def1-234567890123',
        controller: true,
      },
    ],
  },
  spec: {
    claimRef: {
      apiVersion: 'database.example.io/v1alpha1',
      kind: 'PostgreSQLInstance',
      name: 'production-db',
      namespace: 'default',
    },
    compositionRef: {
      name: 'xpostgresqlinstances.aws.database.example.io',
    },
    compositionRevisionRef: {
      name: 'xpostgresqlinstances.aws.database.example.io-v1.2.0',
    },
    compositionSelector: {
      matchLabels: {
        provider: 'aws',
        type: 'postgresql',
      },
    },
    compositionUpdatePolicy: 'Automatic',
    resourceRefs: [
      {
        apiVersion: 'rds.aws.crossplane.io/v1alpha1',
        kind: 'DBInstance',
        name: 'production-db-rds',
      },
      {
        apiVersion: 'ec2.aws.crossplane.io/v1beta1',
        kind: 'SecurityGroup',
        name: 'production-db-sg',
      },
      {
        apiVersion: 'ec2.aws.crossplane.io/v1beta1',
        kind: 'SecurityGroupRule',
        name: 'production-db-ingress',
      },
    ],
    parameters: {
      engineVersion: '14.7',
      instanceClass: 'db.t3.large',
      allocatedStorage: 100,
      maxAllocatedStorage: 1000,
      storageType: 'gp3',
      storageEncrypted: true,
      kmsKeyId: 'arn:aws:kms:us-east-1:example-account:key/example-key',
      iops: 3000,
      multiAZ: true,
      availabilityZone: 'us-east-1a',
      backupRetentionPeriod: 30,
      preferredBackupWindow: '03:00-04:00',
      preferredMaintenanceWindow: 'sun:04:00-sun:05:00',
      deletionProtection: true,
      autoMinorVersionUpgrade: true,
      performanceInsightsEnabled: true,
      performanceInsightsRetentionPeriod: 7,
      enabledCloudwatchLogsExports: ['postgresql', 'upgrade'],
      monitoringInterval: 60,
      monitoringRoleArn: 'arn:aws:iam::example-account:role/example-role',
      tags: {
        Environment: 'production',
        Team: 'platform',
        CostCenter: 'engineering',
        ManagedBy: 'crossplane',
      },
      networkConfig: {
        vpcId: 'vpc-abc123',
        subnetIds: ['subnet-a', 'subnet-b', 'subnet-c'],
        publiclyAccessible: false,
        vpcSecurityGroupIds: ['sg-123abc', 'sg-456def'],
      },
      parameterGroup: {
        name: 'production-postgres-14',
        family: 'postgres14',
        parameters: {
          'max_connections': '200',
          'shared_buffers': '256MB',
          'effective_cache_size': '1GB',
          'maintenance_work_mem': '64MB',
          'checkpoint_completion_target': '0.9',
          'wal_buffers': '16MB',
          'default_statistics_target': '100',
          'random_page_cost': '1.1',
          'effective_io_concurrency': '200',
          'work_mem': '4MB',
          'min_wal_size': '1GB',
          'max_wal_size': '4GB',
        },
      },
      optionGroup: {
        name: 'production-postgres-options',
        engineName: 'postgres',
        majorEngineVersion: '14',
      },
    },
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'True',
        lastTransitionTime: '2024-01-15T09:15:00Z',
        reason: 'Available',
        message: 'Resource is available for use',
      },
      {
        type: 'Synced',
        status: 'True',
        lastTransitionTime: '2024-01-15T09:14:00Z',
        reason: 'ReconcileSuccess',
        message: 'Successfully reconciled resource',
      },
    ],
    connectionDetails: {
      lastPublishedTime: '2024-01-15T09:15:00Z',
    },
  },
};

const colorGroups = {
  Backgrounds: [
    { name: '--bg-primary', label: 'Primary' },
    { name: '--bg-secondary', label: 'Secondary' },
    { name: '--bg-tertiary', label: 'Tertiary' },
    { name: '--bg-hover', label: 'Hover' },
    { name: '--bg-active', label: 'Active' },
  ],
  Text: [
    { name: '--text-primary', label: 'Primary' },
    { name: '--text-secondary', label: 'Secondary' },
    { name: '--text-muted', label: 'Muted' },
  ],
  Status: [
    { name: '--success', label: 'Success' },
    { name: '--warning', label: 'Warning' },
    { name: '--danger', label: 'Danger' },
    { name: '--accent', label: 'Accent' },
  ],
  Other: [
    { name: '--border', label: 'Border' },
    { name: '--accent-hover', label: 'Accent Hover' },
  ],
};

const fontSizes = [
  { name: '--font-xxs', label: '10px (xxs)' },
  { name: '--font-xs', label: '11px (xs)' },
  { name: '--font-sm', label: '12px (sm)' },
  { name: '--font-base', label: '13px (base)' },
  { name: '--font-md', label: '14px (md)' },
  { name: '--font-lg', label: '15px (lg)' },
  { name: '--font-xl', label: '17px (xl)' },
];

export const ComponentLibrary = ({ onClose }: ComponentLibraryProps) => {
  return (
    <div className="component-library-overlay" onClick={onClose}>
      <div className="component-library-modal" onClick={(e) => e.stopPropagation()}>
        <div className="component-library-header">
          <h2>Component Library</h2>
          <button className="component-library-close" onClick={onClose} title="Close">
            <Cross2Icon />
          </button>
        </div>

        <Tabs.Root defaultValue="colors" className="component-library-tabs">
          <Tabs.List className="component-library-tabs-list">
            <Tabs.Trigger value="colors" className="component-library-tab">
              Colors
            </Tabs.Trigger>
            <Tabs.Trigger value="typography" className="component-library-tab">
              Typography
            </Tabs.Trigger>
            <Tabs.Trigger value="components" className="component-library-tab">
              Components
            </Tabs.Trigger>
            <Tabs.Trigger value="inputs" className="component-library-tab">
              Inputs
            </Tabs.Trigger>
            <Tabs.Trigger value="sidebar" className="component-library-tab">
              Sidebar
            </Tabs.Trigger>
          </Tabs.List>

          <Tabs.Content value="colors" className="component-library-content">
            {Object.entries(colorGroups).map(([groupName, colors]) => (
              <div key={groupName} className="color-group">
                <h3>{groupName}</h3>
                <div className="color-grid">
                  {colors.map(({ name, label }) => (
                    <div key={name} className="color-item">
                      <div
                        className="color-swatch"
                        style={{ backgroundColor: `var(${name})` }}
                      />
                      <div className="color-info">
                        <div className="color-label">{label}</div>
                        <div className="color-var">{name}</div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </Tabs.Content>

          <Tabs.Content value="typography" className="component-library-content">
            <div className="typography-section">
              <h3>Font Sizes</h3>
              {fontSizes.map(({ name, label }) => (
                <div key={name} className="typography-item">
                  <span className="typography-label">{label}</span>
                  <span className="typography-sample" style={{ fontSize: `var(${name})` }}>
                    The quick brown fox jumps over the lazy dog
                  </span>
                </div>
              ))}
            </div>

            <div className="typography-section">
              <h3>Line Heights</h3>
              <div className="typography-item">
                <span className="typography-label">Tight (1.3)</span>
                <div style={{ lineHeight: 'var(--line-height-tight)', maxWidth: '400px', color: 'var(--text-primary)' }}>
                  This is sample text with tight line height. It's compact and space-efficient
                  for dense information displays.
                </div>
              </div>
              <div className="typography-item">
                <span className="typography-label">Base (1.55)</span>
                <div style={{ lineHeight: 'var(--line-height-base)', maxWidth: '400px', color: 'var(--text-primary)' }}>
                  This is sample text with base line height. It's the default spacing used
                  throughout the application.
                </div>
              </div>
              <div className="typography-item">
                <span className="typography-label">Relaxed (1.65)</span>
                <div style={{ lineHeight: 'var(--line-height-relaxed)', maxWidth: '400px', color: 'var(--text-primary)' }}>
                  This is sample text with relaxed line height. It's more spacious and easier
                  to read for longer content.
                </div>
              </div>
            </div>

            <div className="typography-section">
              <h3>Monospace Font</h3>
              <div className="typography-item">
                <code style={{ fontFamily: 'var(--font-mono)' }}>
                  kubectl get pods -n default
                </code>
              </div>
            </div>
          </Tabs.Content>

          <Tabs.Content value="components" className="component-library-content">
            <div className="component-section">
              <h3>Status Indicators</h3>
              <div className="component-examples">
                <div className="component-item">
                  <StatusIndicator type="pod" items={[{ name: 'pod-1', ready: true, phase: 'Running' }]} />
                  <span>Running</span>
                </div>
                <div className="component-item">
                  <StatusIndicator type="pod" items={[{ name: 'pod-1', ready: false, phase: 'Pending' }]} />
                  <span>Pending</span>
                </div>
                <div className="component-item">
                  <StatusIndicator type="pod" items={[{ name: 'pod-1', ready: false, phase: 'Failed' }]} />
                  <span>Failed</span>
                </div>
                <div className="component-item">
                  <StatusIndicator type="pod" items={[{ name: 'pod-1', ready: true, phase: 'Succeeded' }]} />
                  <span>Succeeded</span>
                </div>
                <div className="component-item">
                  <StatusIndicator type="pod" items={[{ name: 'pod-1', ready: false, phase: 'Running', reason: 'CrashLoopBackOff' }]} />
                  <span>CrashLoopBackOff</span>
                </div>
              </div>
            </div>

            <div className="component-section">
              <h3>Buttons</h3>
              <div className="component-examples">
                <button className="component-item-btn">Default Button</button>
                <button className="component-item-btn" style={{ backgroundColor: 'var(--accent)', color: 'white' }}>
                  Primary Button
                </button>
                <button className="component-item-btn" style={{ backgroundColor: 'var(--danger)', color: 'white' }}>
                  Danger Button
                </button>
                <button className="component-item-btn" disabled>
                  Disabled Button
                </button>
              </div>
            </div>

            <div className="component-section">
              <h3>Badges</h3>
              <div className="component-examples">
                <span className="badge">Default</span>
                <span className="badge badge-success">Success</span>
                <span className="badge badge-warning">Warning</span>
                <span className="badge badge-danger">Danger</span>
              </div>
            </div>

            <div className="component-section">
              <h3>Shadows</h3>
              <div className="component-examples">
                <div className="shadow-box" style={{ boxShadow: 'var(--shadow-sm)' }}>
                  Small Shadow
                </div>
                <div className="shadow-box" style={{ boxShadow: 'var(--shadow-md)' }}>
                  Medium Shadow
                </div>
                <div className="shadow-box" style={{ boxShadow: 'var(--shadow-lg)' }}>
                  Large Shadow
                </div>
              </div>
            </div>
          </Tabs.Content>

          <Tabs.Content value="inputs" className="component-library-content">
            <div className="component-section">
              <h3>Text Inputs</h3>
              <div className="input-examples">
                <input type="text" placeholder="Default input" className="input-example" />
                <input type="text" placeholder="Disabled input" className="input-example" disabled />
              </div>
            </div>

            <div className="component-section">
              <h3>Search Input</h3>
              <div className="input-examples">
                <input
                  type="search"
                  placeholder="Search resources..."
                  className="input-example search-input"
                />
              </div>
            </div>

            <div className="component-section">
              <h3>Checkboxes & Radio</h3>
              <div className="input-examples">
                <label className="checkbox-label">
                  <input type="checkbox" />
                  <span>Checkbox option</span>
                </label>
                <label className="checkbox-label">
                  <input type="checkbox" checked readOnly />
                  <span>Checked checkbox</span>
                </label>
                <label className="radio-label">
                  <input type="radio" name="example" />
                  <span>Radio option 1</span>
                </label>
                <label className="radio-label">
                  <input type="radio" name="example" />
                  <span>Radio option 2</span>
                </label>
              </div>
            </div>

            <div className="component-section">
              <h3>Select Dropdown</h3>
              <div className="input-examples">
                <select className="input-example">
                  <option>Select an option</option>
                  <option>Option 1</option>
                  <option>Option 2</option>
                  <option>Option 3</option>
                </select>
              </div>
            </div>
          </Tabs.Content>

          <Tabs.Content value="sidebar" className="component-library-content">
            <div className="component-section">
              <h3>Loading States</h3>
              <ResizableContainer className="detail-skeleton-container">
                <ResourceDetailSkeleton
                  resource={{ kind: 'Pod', apiVersion: 'v1', metadata: { name: 'example-pod', namespace: 'default' } }}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Pod Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyPodData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Deployment Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyDeploymentData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Custom Resource Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyCustomResourceData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Service Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyServiceData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>ConfigMap Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyConfigMapData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Secret Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummySecretData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Endpoints Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyEndpointsData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Role Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyRoleData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>RoleBinding Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyRoleBindingData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>

            <div className="component-section">
              <h3>Node Detail View</h3>
              <ResizableContainer className="detail-view-container">
                <ResourceDetailView
                  resource={dummyNodeData}
                  cluster="demo-cluster"
                  actions={null}
                  mode="detail"
                />
              </ResizableContainer>
            </div>
          </Tabs.Content>
        </Tabs.Root>
      </div>
    </div>
  );
};
