/**
 * 实时列车 WebSocket 客户端（见 FRONTEND_PROMPT.md §4.8）。
 * 连接 /api/v1/realtime，处理 snapshot/update/remove，断线自动重连。
 */
import { useEffect } from 'react';
import { getConfig } from '../config';
import { useStore } from '../store/useStore';
import type { Train, WsEnvelope } from '../types';

export function useRealtime(online: boolean) {
  const upsertTrains = useStore((s) => s.upsertTrains);
  const removeTrains = useStore((s) => s.removeTrains);

  useEffect(() => {
    if (!online) return;
    let ws: WebSocket | null = null;
    let closed = false;
    let retry: ReturnType<typeof setTimeout> | null = null;

    const connect = () => {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws';
      ws = new WebSocket(`${proto}://${location.host}${getConfig().realtimeWsPath}`);

      ws.onmessage = (ev) => {
        let env: WsEnvelope;
        try {
          env = JSON.parse(ev.data as string);
        } catch {
          return;
        }
        switch (env.type) {
          case 'snapshot':
          case 'update':
            upsertTrains((env.data as Train[]) ?? []);
            break;
          case 'remove': {
            const data = env.data as unknown;
            const ids = Array.isArray(data)
              ? data.map((d) => (typeof d === 'string' ? d : (d as { trainId: string }).trainId))
              : [];
            removeTrains(ids);
            break;
          }
          // ping/pong：浏览器自动处理，无需应答
        }
      };

      ws.onclose = () => {
        if (!closed) retry = setTimeout(connect, 3000);
      };
      ws.onerror = () => ws?.close();
    };

    connect();
    return () => {
      closed = true;
      if (retry) clearTimeout(retry);
      ws?.close();
    };
  }, [online, upsertTrains, removeTrains]);
}
