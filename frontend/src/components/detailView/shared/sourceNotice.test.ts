import { describe, expect, it } from 'vitest';
import { cachedCopyNotice } from './sourceNotice';

describe('cachedCopyNotice', () => {
  it('says the object is gone on a 404', () => {
    expect(cachedCopyNotice({ response: { status: 404 } }).kind).toBe('deleted');
  });

  it('says the copy is cached, and why, on any other failure', () => {
    const notice = cachedCopyNotice({ response: { status: 500, data: { error: 'etcd timeout' } } });
    expect(notice.kind).toBe('cached');
    expect(notice.message).toContain('etcd timeout');
    expect(cachedCopyNotice(new Error('Network Error')).message).toContain('Network Error');
  });
});
