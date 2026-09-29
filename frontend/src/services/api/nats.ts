import logger from '../../utils/logger';
import { NatsDetection, NatsOverview } from '../../types/nats';
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
