import type { RoutePath, FeatureCollection } from '../types';
import type { ComputeInput } from './compute';

/** 路线查询超时错误，供 UI 区分「超时」与其它失败。 */
export class RouteSearchTimeoutError extends Error {
  constructor(ms: number) {
    super(`route search timed out after ${ms}ms`);
    this.name = 'RouteSearchTimeoutError';
  }
}

type Pending = {
  resolve: (v: RoutePath[]) => void;
  reject: (e: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};

/**
 * 寻路 Worker 客户端：在后台线程建图与寻路，主线程界面不卡死。
 * 每次 query 带自增 id 防竞态；超时则终止并重建 Worker（终止正在跑的同步计算）。
 */
export class RouteClient {
  private worker: Worker | null = null;
  private geojson: FeatureCollection | null = null;
  private seq = 0;
  private pending = new Map<number, Pending>();

  /** 用最新数据（重）初始化 Worker。数据变化（如切世界重载）时调用。 */
  init(geojson: FeatureCollection) {
    this.geojson = geojson;
    this.spawn();
  }

  private spawn() {
    this.dispose();
    // 终止旧 Worker 后，其上未完成的查询永远不会有响应；清理并拒绝，避免定时器悬挂。
    this.failAll(new Error('route worker restarted'));
    if (!this.geojson) return;
    const worker = new Worker(new URL('./route.worker.ts', import.meta.url), { type: 'module' });
    worker.onmessage = (e: MessageEvent) => this.onMessage(e.data);
    worker.onerror = () => this.failAll(new Error('route worker crashed'));
    worker.postMessage({ type: 'init', geojson: this.geojson });
    this.worker = worker;
  }

  private onMessage(msg: { type: string; id?: number; candidates?: RoutePath[]; message?: string }) {
    if (msg.type === 'ready') return;
    if (msg.id === undefined) return;
    const p = this.pending.get(msg.id);
    if (!p) return; // 已超时/被取代，忽略迟到结果
    clearTimeout(p.timer);
    this.pending.delete(msg.id);
    if (msg.type === 'result') p.resolve(msg.candidates ?? []);
    else p.reject(new Error(msg.message ?? 'route search failed'));
  }

  private failAll(err: Error) {
    for (const p of this.pending.values()) {
      clearTimeout(p.timer);
      p.reject(err);
    }
    this.pending.clear();
  }

  /**
   * 发起一次寻路。timeoutMs 内无结果则 reject(RouteSearchTimeoutError) 并重建 Worker，
   * 使正在后台运行的同步计算被真正终止，界面回到可交互状态。
   */
  query(input: ComputeInput, timeoutMs: number): Promise<RoutePath[]> {
    if (!this.worker) this.spawn();
    if (!this.worker) return Promise.reject(new Error('route worker unavailable'));
    const id = ++this.seq;
    const worker = this.worker;
    return new Promise<RoutePath[]>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        this.spawn(); // 终止卡住的计算并重建，供后续查询使用
        reject(new RouteSearchTimeoutError(timeoutMs));
      }, timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      worker.postMessage({ type: 'query', id, input });
    });
  }

  private dispose() {
    if (this.worker) {
      this.worker.terminate();
      this.worker = null;
    }
  }
}

/** 全局单例：与 store 生命周期一致。 */
export const routeClient = new RouteClient();
