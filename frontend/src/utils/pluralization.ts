/**
 * Kubernetes resource pluralization utilities
 * Handles the standard Kubernetes naming conventions
 */

/**
 * Convert a Kubernetes resource kind to its plural form
 * Following Kubernetes API conventions
 */
export function pluralize(singular: string): string {
  const lower = singular.toLowerCase();

  // Special cases that don't follow standard rules
  const irregulars: Record<string, string> = {
    ingress: 'ingresses',
    networkpolicy: 'networkpolicies',
    resourcequota: 'resourcequotas',
    limitrange: 'limitranges',
    podtemplate: 'podtemplates',
    poddisruptionbudget: 'poddisruptionbudgets',
    horizontalpodautoscaler: 'horizontalpodautoscalers',
  };

  if (irregulars[lower]) {
    return irregulars[lower];
  }

  // Already plural (ends with 's' but not 'ss')
  if (lower.endsWith('s') && !lower.endsWith('ss')) {
    return lower;
  }

  // Words ending in 'y' -> 'ies' (like policy -> policies)
  if (
    lower.endsWith('y') &&
    !['ay', 'ey', 'iy', 'oy', 'uy'].some((v) => lower.endsWith(v))
  ) {
    return lower.slice(0, -1) + 'ies';
  }

  // Words ending in 's', 'ss', 'x', 'z', 'ch', 'sh' -> add 'es'
  if (
    lower.endsWith('s') ||
    lower.endsWith('ss') ||
    lower.endsWith('x') ||
    lower.endsWith('z') ||
    lower.endsWith('ch') ||
    lower.endsWith('sh')
  ) {
    return lower + 'es';
  }

  // Default: add 's'
  return lower + 's';
}

/**
 * Convert a plural resource name back to singular
 */
export function singularize(plural: string): string {
  const lower = plural.toLowerCase();

  // Special cases
  const irregulars: Record<string, string> = {
    ingresses: 'ingress',
    networkpolicies: 'networkpolicy',
    resourcequotas: 'resourcequota',
    limitranges: 'limitrange',
    podtemplates: 'podtemplate',
    poddisruptionbudgets: 'poddisruptionbudget',
    horizontalpodautoscalers: 'horizontalpodautoscaler',
  };

  if (irregulars[lower]) {
    return irregulars[lower];
  }

  // Words ending in 'ies' -> 'y'
  if (lower.endsWith('ies')) {
    return lower.slice(0, -3) + 'y';
  }

  // Words ending in 'es' after s/x/z/ch/sh
  if (
    lower.endsWith('ses') ||
    lower.endsWith('sses') ||
    lower.endsWith('xes') ||
    lower.endsWith('zes') ||
    lower.endsWith('ches') ||
    lower.endsWith('shes')
  ) {
    return lower.slice(0, -2);
  }

  // Words ending in 's'
  if (lower.endsWith('s')) {
    return lower.slice(0, -1);
  }

  // Already singular
  return lower;
}
