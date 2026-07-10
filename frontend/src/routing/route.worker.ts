/// <reference lib="webworker" />
import { RouteGraph } from './graph';
import { computeCandidates, type ComputeInput } from './compute';
import type { RoutePath, FeatureCollection } from '../types';

/** init：用 geojson 建图（一次）。query：跑寻路回传候选。 */
type InMessage =
  | { type: 'init'; geojson: FeatureCollection }
  | { type: 'query'; id: number; input: ComputeInput };

type OutMessage =
  | { type: 'ready' }
  | { type: 'result'; id: number; candidates: RoutePath[] }
  | { type: 'error'; id: number; message: string };

let graph: RouteGraph | null = null;

self.onmessage = (e: MessageEvent<InMessage>) => {
  const msg = e.data;
  if (msg.type === 'init') {
    graph = RouteGraph.fromFeatureCollection(msg.geojson);
    (self as DedicatedWorkerGlobalScope).postMessage({ type: 'ready' } as OutMessage);
    return;
  }
  if (msg.type === 'query') {
    try {
      if (!graph) throw new Error('graph not initialized');
      const candidates = computeCandidates(graph, msg.input);
      (self as DedicatedWorkerGlobalScope).postMessage({ type: 'result', id: msg.id, candidates } as OutMessage);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      (self as DedicatedWorkerGlobalScope).postMessage({ type: 'error', id: msg.id, message } as OutMessage);
    }
  }
};
