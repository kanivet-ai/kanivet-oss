import { describe, it, expect, vi } from 'vitest';

const links = vi.hoisted(() => ({
  NodeLink: () => null,
  OwnerLink: () => null,
  ServiceAccountLink: () => null,
}));
vi.mock('../components/NodeLink', () => ({ default: links.NodeLink }));
vi.mock('../components/OwnerLink', () => ({ default: links.OwnerLink }));
vi.mock('../components/ServiceAccountLink', () => ({
  default: links.ServiceAccountLink,
}));

const {
  formatContainerRuntime,
  formatNodeKernelVersion,
  formatNodeName,
  formatNodeOSImage,
  formatNodeVersion,
  formatOwner,
  formatServiceAccount,
} = await import('./columnFormatters');

// The renderer has no `require`: these formatters must reach the link
// components through imports.
describe('link column formatters', () => {
  it('render the node, owner and service account links', () => {
    expect(formatNodeName({ nodeName: 'node-1' }).type).toBe(links.NodeLink);
    expect(formatOwner({ ownerReferences: [] }).type).toBe(links.OwnerLink);
    const account = formatServiceAccount({ namespace: 'ns' });
    expect(account.type).toBe(links.ServiceAccountLink);
    expect(account.props).toEqual({
      serviceAccountName: 'default',
      namespace: 'ns',
    });
  });
});

// Node items carry status.nodeInfo as-is (backend listadapters/cluster.go).
describe('node column formatters', () => {
  const node = {
    kind: 'Node',
    nodeInfo: {
      kubeletVersion: 'v1.34.1-eks-113cf36',
      osImage: 'Bottlerocket OS 1.45.0 (aws-k8s-1.34)',
      kernelVersion: '6.12.40',
      containerRuntimeVersion: 'containerd://2.0.6+bottlerocket',
    },
  };

  it('read node details from nodeInfo', () => {
    expect(formatNodeVersion(node)).toBe('v1.34.1-eks-113cf36');
    expect(formatNodeOSImage(node)).toBe('Bottlerocket OS 1.45.0 (aws-k8s-1.34)');
    expect(formatNodeKernelVersion(node)).toBe('6.12.40');
    expect(formatContainerRuntime(node)).toBe('containerd://2.0.6+bottlerocket');
  });

  it('keep a top-level version for other kinds in the VERSION column', () => {
    expect(formatNodeVersion({ kind: 'APIService', version: 'v1beta1' })).toBe('v1beta1');
  });

  it('fall back to a dash without nodeInfo', () => {
    expect(formatNodeVersion({ kind: 'Node' })).toBe('-');
    expect(formatNodeOSImage({ kind: 'Node' })).toBe('-');
  });
});
