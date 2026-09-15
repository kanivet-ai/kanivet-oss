import { apiClient } from './client';
import {
  AWSCredentials,
  AWSCredentialsOverride,
  AWSIdentitiesResponse,
  AWSIdentityClusterStatus,
  AWSIdentityExplanation,
  AWSSimulateRequest,
  AWSSimulateResponse,
} from '../../types/awsIdentity';

const BASE = '/cluster/aws/identity';

export interface AWSIdentityTarget {
  namespace: string;
  serviceAccount?: string;
  pod?: string;
}

export async function getAWSIdentityStatus(
  cluster: string,
): Promise<AWSIdentityClusterStatus> {
  return await apiClient.request(`${BASE}/status`, { cluster }, true);
}

export async function explainAWSIdentity(
  cluster: string,
  target: AWSIdentityTarget,
): Promise<AWSIdentityExplanation> {
  const params: Record<string, string> = {
    cluster,
    namespace: target.namespace,
  };
  if (target.serviceAccount) params.serviceAccount = target.serviceAccount;
  if (target.pod) params.pod = target.pod;
  return await apiClient.request(`${BASE}/explain`, params, false);
}

export async function listAWSIdentities(
  cluster: string,
): Promise<AWSIdentitiesResponse> {
  return await apiClient.request(`${BASE}/identities`, { cluster }, false);
}

export async function simulateAWSAccess(
  cluster: string,
  request: AWSSimulateRequest,
): Promise<AWSSimulateResponse> {
  const response = await apiClient
    .getAxios()
    .post(`${BASE}/simulate`, request, { params: { cluster } });
  return response.data;
}

export async function getAWSIdentityCredentials(
  cluster: string,
): Promise<AWSCredentials> {
  return await apiClient.request(`${BASE}/credentials`, { cluster }, false);
}

export async function setAWSIdentityCredentials(
  cluster: string,
  override: AWSCredentialsOverride,
): Promise<AWSCredentials> {
  const response = await apiClient
    .getAxios()
    .put(`${BASE}/credentials`, override, { params: { cluster } });
  apiClient.invalidateCache(BASE);
  return response.data;
}

export async function clearAWSIdentityCredentials(
  cluster: string,
): Promise<AWSCredentials> {
  const response = await apiClient
    .getAxios()
    .delete(`${BASE}/credentials`, { params: { cluster } });
  apiClient.invalidateCache(BASE);
  return response.data;
}
