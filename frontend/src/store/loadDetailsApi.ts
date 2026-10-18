import api from '../services/api';
import type { ResourceDetailsOptions, ResourceEventsOptions } from '../services/api/resources';

/*
 * The detail loaders' requests, with the options services/api/resources.ts
 * takes after the abort signal (a fresh read past the backend's detail cache;
 * the uid, Kind and apiVersion that keep another object's events out). They
 * go through the `api` facade like every other store request.
 */

type DetailsRequest = (
  cluster: string, group: string, version: string, kind: string, namespace: string, name: string,
  signal?: AbortSignal, options?: ResourceDetailsOptions,
) => Promise<any>;
type EventsRequest = (
  cluster: string, group: string, version: string, kind: string, namespace: string, name: string,
  signal?: AbortSignal, options?: ResourceEventsOptions,
) => Promise<any[]>;

export const fetchResourceDetails: DetailsRequest = (...args) =>
  (api.getResourceDetails as DetailsRequest).apply(api, args);

export const fetchResourceEvents: EventsRequest = (...args) =>
  (api.getResourceEvents as EventsRequest).apply(api, args);
