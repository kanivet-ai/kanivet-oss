import logger from '../../utils/logger';
import {
  NatsConsumerInfoFull,
  NatsDetection,
  NatsKVEntry,
  NatsObjectInfo,
  NatsOverview,
  NatsRawMessage,
} from '../../types/nats';
import { apiClient } from './client';

export async function getNatsDetection(
  cluster: string,
): Promise<NatsDetection> {
  try {
    const response = await apiClient
      .getAxios()
      .get('/cluster/nats/detect', { params: { cluster } });
    return response.data.detection;
  } catch (error) {
    logger.error('Failed to get NATS detection', { error, cluster });
    throw error;
  }
}

export async function getNatsOverview(cluster: string): Promise<NatsOverview> {
  try {
    const response = await apiClient
      .getAxios()
      .get('/cluster/nats/overview', { params: { cluster } });
    return response.data.overview;
  } catch (error) {
    logger.error('Failed to get NATS overview', { error, cluster });
    throw error;
  }
}

export async function getNatsStreamMessage(
  cluster: string,
  stream: string,
  seq: number,
): Promise<NatsRawMessage> {
  try {
    const response = await apiClient
      .getAxios()
      .get(`/cluster/nats/stream/${stream}/message`, {
        params: { cluster, seq },
      });
    return response.data.message;
  } catch (error) {
    logger.error('Failed to get NATS stream message', {
      error,
      cluster,
      stream,
      seq,
    });
    throw error;
  }
}

export async function getNatsStreamLastMessage(
  cluster: string,
  stream: string,
  subject: string,
): Promise<NatsRawMessage> {
  try {
    const response = await apiClient
      .getAxios()
      .get(`/cluster/nats/stream/${stream}/last-message`, {
        params: { cluster, subject },
      });
    return response.data.message;
  } catch (error) {
    logger.error('Failed to get NATS last stream message', {
      error,
      cluster,
      stream,
      subject,
    });
    throw error;
  }
}

export async function getNatsConsumerInfo(
  cluster: string,
  stream: string,
  consumer: string,
): Promise<NatsConsumerInfoFull> {
  try {
    const response = await apiClient
      .getAxios()
      .get(`/cluster/nats/stream/${stream}/consumer/${consumer}`, {
        params: { cluster },
      });
    return response.data.consumer;
  } catch (error) {
    logger.error('Failed to get NATS consumer info', {
      error,
      cluster,
      stream,
      consumer,
    });
    throw error;
  }
}

export async function listNatsKVBuckets(cluster: string): Promise<string[]> {
  try {
    const response = await apiClient
      .getAxios()
      .get('/cluster/nats/kv', { params: { cluster } });
    return response.data.buckets ?? [];
  } catch (error) {
    logger.error('Failed to list NATS KV buckets', { error, cluster });
    throw error;
  }
}

export async function listNatsKVKeys(
  cluster: string,
  bucket: string,
): Promise<string[]> {
  try {
    const response = await apiClient
      .getAxios()
      .get(`/cluster/nats/kv/${bucket}/keys`, { params: { cluster } });
    return response.data.keys ?? [];
  } catch (error) {
    logger.error('Failed to list NATS KV keys', { error, cluster, bucket });
    throw error;
  }
}

export async function getNatsKVEntry(
  cluster: string,
  bucket: string,
  key: string,
): Promise<NatsKVEntry> {
  try {
    const response = await apiClient
      .getAxios()
      .get(`/cluster/nats/kv/${bucket}/keys/${key}`, { params: { cluster } });
    return response.data.entry;
  } catch (error) {
    logger.error('Failed to get NATS KV entry', {
      error,
      cluster,
      bucket,
      key,
    });
    throw error;
  }
}

export async function getNatsKVHistory(
  cluster: string,
  bucket: string,
  key: string,
): Promise<NatsKVEntry[]> {
  try {
    const response = await apiClient
      .getAxios()
      .get(`/cluster/nats/kv/${bucket}/keys/${key}/history`, {
        params: { cluster },
      });
    return response.data.history ?? [];
  } catch (error) {
    logger.error('Failed to get NATS KV history', {
      error,
      cluster,
      bucket,
      key,
    });
    throw error;
  }
}

export async function listNatsObjectStores(cluster: string): Promise<string[]> {
  try {
    const response = await apiClient
      .getAxios()
      .get('/cluster/nats/objectstore', { params: { cluster } });
    return response.data.buckets ?? [];
  } catch (error) {
    logger.error('Failed to list NATS object stores', { error, cluster });
    throw error;
  }
}

export async function listNatsObjects(
  cluster: string,
  bucket: string,
): Promise<NatsObjectInfo[]> {
  try {
    const response = await apiClient
      .getAxios()
      .get(`/cluster/nats/objectstore/${bucket}/objects`, {
        params: { cluster },
      });
    return response.data.objects ?? [];
  } catch (error) {
    logger.error('Failed to list NATS objects', { error, cluster, bucket });
    throw error;
  }
}

/**
 * Downloads go through the authenticated axios client, not a plain link or
 * window.open - every request needs the X-Session-Secret header the request
 * interceptor attaches, which a bare URL would never carry.
 */
export async function downloadNatsObject(
  cluster: string,
  bucket: string,
  name: string,
): Promise<Blob> {
  try {
    const response = await apiClient
      .getAxios()
      .get(
        `/cluster/nats/objectstore/${bucket}/objects/${encodeURIComponent(name)}`,
        {
          params: { cluster },
          responseType: 'blob',
        },
      );
    return response.data;
  } catch (error) {
    logger.error('Failed to download NATS object', {
      error,
      cluster,
      bucket,
      name,
    });
    throw error;
  }
}
