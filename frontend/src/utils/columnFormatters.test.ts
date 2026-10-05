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

const { formatNodeName, formatOwner, formatServiceAccount } = await import(
  './columnFormatters'
);

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
