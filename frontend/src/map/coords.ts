/**
 * 坐标变换：游戏坐标 (x, z) ↔ MapLibre 经纬度 (lng, lat)。
 *
 * 采用「墨卡托像素线性映射」：把游戏平面等比铺到墨卡托像素平面，并以赤道中心
 * (lng0 / lat0) 作为原点。这样做的好处：
 * - 游戏 x/z 用同一比例换算，形状不变形（等比）；
 * - 矢量线路与栅格瓦片落在同一像素金字塔上，天然对齐；
 * - 数据集中在赤道附近，远离墨卡托两极畸变与 ±85° 截断区。
 *
 * config.yml 只暴露两个对准瓦片用的旋钮：
 * - mapScale:  1 个游戏方块 = 多少「原生瓦片像素」（整体缩放，默认 1）。
 * - mapOffset: [x, z] 游戏坐标整体平移（游戏单位，默认 [0, 0]）。
 */
import { getConfig } from '../config';
import type { WorldTileConfig } from '../types';

const DEFAULT_MAP_SCALE = 1;
const MAX_MERCATOR_LAT = 85.0511287798066;

export function gameToLngLat(x: number, z: number, world?: string): [number, number] {
  const tile = world ? getConfig().worldTiles[world] : undefined;
  const [px, py] = gameToPixel(x, z, tile);
  return pixelToLngLat(px, py, nativeZoom(tile), tileSize(tile));
}

export function lngLatToGame(lng: number, lat: number, world?: string): [number, number] {
  const tile = world ? getConfig().worldTiles[world] : undefined;
  const [px, py] = lngLatToPixel(lng, lat, nativeZoom(tile), tileSize(tile));
  return pixelToGame(px, py, tile);
}

/** 把一段游戏坐标顶点序列（[x,z,y...]）转为经纬度数组，供 MapLibre LineString 使用。 */
export function gameLineToLngLat(coords: number[][], world?: string): [number, number][] {
  return coords.map((c) => gameToLngLat(c[0], c[1], world));
}

function nativeZoom(tile?: WorldTileConfig): number {
  return tile?.maxNativeZoom ?? tile?.zoom ?? 0;
}

function tileSize(tile?: WorldTileConfig): number {
  return tile?.tileSize ?? 256;
}

function mapScale(tile?: WorldTileConfig): number {
  const s = tile?.mapScale;
  return s && s > 0 ? s : DEFAULT_MAP_SCALE;
}

function mapOffset(tile?: WorldTileConfig): [number, number] {
  const o = tile?.mapOffset;
  return o && o.length >= 2 ? [o[0], o[1]] : [0, 0];
}

/** 游戏坐标 → 墨卡托像素。原点(经 offset 平移后)落在世界中心，即赤道 lng0/lat0。 */
function gameToPixel(x: number, z: number, tile?: WorldTileConfig): [number, number] {
  const center = (tileSize(tile) * 2 ** nativeZoom(tile)) / 2;
  const [offX, offZ] = mapOffset(tile);
  const s = mapScale(tile);
  // 游戏 +x 向东(像素 +x)，+z 向南(像素 +y)。
  return [center + (x + offX) * s, center + (z + offZ) * s];
}

/** 墨卡托像素 → 游戏坐标（gameToPixel 的逆运算）。 */
function pixelToGame(px: number, py: number, tile?: WorldTileConfig): [number, number] {
  const center = (tileSize(tile) * 2 ** nativeZoom(tile)) / 2;
  const [offX, offZ] = mapOffset(tile);
  const s = mapScale(tile);
  return [(px - center) / s - offX, (py - center) / s - offZ];
}

function pixelToLngLat(px: number, py: number, zoom: number, size: number): [number, number] {
  const worldSize = size * 2 ** zoom;
  const lng = (px / worldSize) * 360 - 180;
  const y = Math.PI * (1 - (2 * py) / worldSize);
  const lat = (Math.atan(Math.sinh(y)) * 180) / Math.PI;
  return [lng, clamp(lat, -MAX_MERCATOR_LAT, MAX_MERCATOR_LAT)];
}

function lngLatToPixel(lng: number, lat: number, zoom: number, size: number): [number, number] {
  const worldSize = size * 2 ** zoom;
  const clampedLat = clamp(lat, -MAX_MERCATOR_LAT, MAX_MERCATOR_LAT);
  const latRad = (clampedLat * Math.PI) / 180;
  const px = ((lng + 180) / 360) * worldSize;
  const py = ((1 - Math.log(Math.tan(latRad) + 1 / Math.cos(latRad)) / Math.PI) / 2) * worldSize;
  return [px, py];
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}
