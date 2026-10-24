/**
 * What a view says when it could not load the latest copy of an object and
 * shows the one it already had instead.
 */
export interface SourceNotice {
  /** 'deleted': the cluster answered 404; 'cached': any other failure. */
  kind: 'deleted' | 'cached';
  message: string;
}

export const isNotFound = (error: any): boolean => error?.response?.status === 404;

export const errorMessage = (error: any): string =>
  error?.response?.data?.error || error?.message || 'unknown error';

export const cachedCopyNotice = (error: any): SourceNotice =>
  isNotFound(error)
    ? { kind: 'deleted', message: 'This resource no longer exists in the cluster. Showing the last copy Kanivet had.' }
    : { kind: 'cached', message: `Showing a cached copy. Could not load the latest: ${errorMessage(error)}` };
