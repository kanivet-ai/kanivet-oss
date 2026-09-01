import {
  getPluralKind,
  getApiGroup,
  getApiVersion,
  isNamespaced,
  isNavigable,
} from './k8sResources';

// Re-export with original names for backwards compatibility
export function pluralizeKind(kind: string): string {
  return getPluralKind(kind);
}

export function getGroupForKind(kind: string): string {
  return getApiGroup(kind);
}

export function getVersionForKind(kind: string): string {
  return getApiVersion(kind);
}

export function isNamespacedKind(kind: string): boolean {
  return isNamespaced(kind);
}

export function isNavigableKind(kind: string): boolean {
  return isNavigable(kind);
}
