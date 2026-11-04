import { apiClient } from './client';

/** Where an image runs. Only names go to the backend; it reads pull secrets
 * itself and never returns them. */
export interface ImagePullContext {
  cluster?: string;
  namespace?: string;
  pod?: string;
  container?: string;
  pullSecrets?: string[];
  serviceAccount?: string;
  imageID?: string;
  platform?: string;
}

export interface ImageEnvVar { name: string; value: string; redacted?: boolean }

export interface ImageConfig {
  entrypoint?: string[];
  cmd?: string[];
  env?: ImageEnvVar[];
  user?: string;
  workingDir?: string;
  labels?: Record<string, string>;
  exposedPorts?: string[];
  volumes?: string[];
  stopSignal?: string;
  os?: string;
  architecture?: string;
  author?: string;
}

export interface ImageLayer {
  index: number;
  digest: string;
  diffID?: string;
  size: number;
  mediaType: string;
  createdBy?: string;
  created?: string;
  comment?: string;
}

export interface ImageHistory { createdBy?: string; created?: string; emptyLayer?: boolean; layerIndex: number }

export interface ImageTagStatus {
  tag: string;
  currentDigest?: string;
  runningDigest?: string;
  moved: boolean;
  error?: string;
}

export interface ImageInfo {
  ref: string;
  resolvedRef: string;
  digest: string;
  indexDigest?: string;
  mediaType: string;
  platform: string;
  platforms?: string[];
  created?: string;
  size: number;
  config: ImageConfig;
  layers: ImageLayer[];
  history?: ImageHistory[];
  tag?: ImageTagStatus;
  authMethod: string;
  indexed: boolean;
}

export interface ImageFileEntry {
  name: string;
  path: string;
  type: 'file' | 'dir' | 'symlink' | 'hardlink' | 'other';
  size: number;
  mode: string;
  linkTarget?: string;
  layer: number;
  children?: number;
  totalSize?: number;
}

export interface ImageDirListing { path: string; entries: ImageFileEntry[]; totalFiles: number; totalSize: number }

export interface ImageFileContent {
  path: string;
  size: number;
  content?: string;
  binary: boolean;
  truncated: boolean;
  layer: number;
}

export interface ImageLayerChange { path: string; op: 'added' | 'modified' | 'deleted'; type: string; size: number }

export interface ImageLayerChanges {
  layer: number;
  changes: ImageLayerChange[];
  truncated?: boolean;
  added: number;
  modified: number;
  deleted: number;
}

export interface ImageSearchResult { query: string; entries: ImageFileEntry[]; truncated?: boolean }

export interface ImageIndexStatus {
  ready: boolean;
  indexing: boolean;
  layersDone: number;
  layersTotal: number;
  bytesDone: number;
  bytesTotal: number;
  error?: string;
}

export type ImageErrorKind = 'auth' | 'not_found' | 'unreachable' | 'invalid' | 'too_large' | 'internal';

/** A failed image call, with the backend's classification. */
export class ImageApiError extends Error {
  constructor(message: string, public kind: ImageErrorKind) {
    super(message);
  }
}

function params(ref: string, pc: ImagePullContext, extra: Record<string, string | number | undefined> = {}) {
  const p: Record<string, string> = { ref };
  for (const [k, v] of Object.entries({ ...pc, ...extra })) {
    if (v === undefined || v === null || v === '') continue;
    p[k] = Array.isArray(v) ? v.join(',') : String(v);
  }
  return p;
}

async function get<T>(path: string, ref: string, pc: ImagePullContext, extra?: Record<string, string | number | undefined>, long = false): Promise<T> {
  try {
    const res = await apiClient.getAxios().get(path, { params: params(ref, pc, extra), long, timeout: long ? 300_000 : 60_000 });
    return res.data as T;
  } catch (e: any) {
    const data = e?.response?.data;
    if (data?.error) throw new ImageApiError(data.error, (data.kind || 'internal') as ImageErrorKind);
    throw new ImageApiError(e?.message || 'Request failed', 'unreachable');
  }
}

export const getImageInfo = (ref: string, pc: ImagePullContext) => get<ImageInfo>('/images/info', ref, pc);
export const getImageIndexStatus = (ref: string, pc: ImagePullContext) => get<ImageIndexStatus>('/images/status', ref, pc);
export const listImageDir = (ref: string, pc: ImagePullContext, path: string) => get<ImageDirListing>('/images/files', ref, pc, { path }, true);
export const readImageFile = (ref: string, pc: ImagePullContext, path: string, limit?: number) =>
  get<ImageFileContent>('/images/file', ref, pc, { path, limit }, true);
export const searchImageFiles = (ref: string, pc: ImagePullContext, q: string) => get<ImageSearchResult>('/images/search', ref, pc, { q }, true);
export const getImageLayerChanges = (ref: string, pc: ImagePullContext, layer: number) =>
  get<ImageLayerChanges>('/images/layer-changes', ref, pc, { layer }, true);
