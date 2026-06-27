/**
 * 坐标变换：游戏坐标 (x, z) ↔ MapLibre 经纬度 (lng, lat)。
 *
 * 优先使用 worldTiles.<world> 中的瓦片像素映射：
 * - gameTopLeft: 整张瓦片地图左上角对应的游戏坐标 [x, z]
 * - tileTopLeft: 该左上角所在的原生瓦片坐标 [tileX, tileY]，默认 [0, 0]
 * - pixelGameSize: 原生瓦片层级下 1 像素对应的游戏内长度
 * - tileSize / maxNativeZoom: 原生瓦片尺寸与原生瓦片层级
 *
 * 未配置上述字段时，回退到默认 0.01 的线性映射。
 */
import { getConfig } from '../config';
import type { WorldTileConfig } from '../types';

const DEFAULT_COORD_SCALE = 0.01;
const MAX_MERCATOR_LAT = 85.0511287798066;

export function gameToLngLat(x: number, z: number, world?: string): [number, number] {
  const tile = world ? getConfig().worldTiles[world] : undefined;
  if (hasPixelMapping(tile)) {
    const [px, py] = gameToNativePixel(x, z, tile);
    return pixelToLngLat(px, py, nativeZoom(tile), tile.tileSize ?? 256);
  }

  return [x * DEFAULT_COORD_SCALE, -z * DEFAULT_COORD_SCALE];
}

export function lngLatToGame(lng: number, lat: number, world?: string): [number, number] {
  const tile = world ? getConfig().worldTiles[world] : undefined;
  if (hasPixelMapping(tile)) {
    const [px, py] = lngLatToPixel(lng, lat, nativeZoom(tile), tile.tileSize ?? 256);
    return nativePixelToGame(px, py, tile);
  }

  return [lng / DEFAULT_COORD_SCALE, -lat / DEFAULT_COORD_SCALE];
}

/** 把一段游戏坐标顶点序列（[x,z,y...]）转为经纬度数组，供 MapLibre LineString 使用。 */
export function gameLineToLngLat(coords: number[][], world?: string): [number, number][] {
  return coords.map((c) => gameToLngLat(c[0], c[1], world));
}

function hasPixelMapping(tile: WorldTileConfig | undefined): tile is WorldTileConfig {
  return Boolean(tile?.gameTopLeft && tile.gameTopLeft.length >= 2 && tile.pixelGameSize && tile.pixelGameSize > 0);
}

function nativeZoom(tile: WorldTileConfig): number {
  return tile.maxNativeZoom ?? tile.zoom ?? 0;
}

function gameToNativePixel(x: number, z: number, tile: WorldTileConfig): [number, number] {
  const tileSize = tile.tileSize ?? 256;
  const [leftX, topZ] = tile.gameTopLeft ?? [0, 0];
  const [tileX, tileY] = tile.tileTopLeft ?? [0, 0];
  const px = tileX * tileSize + (x - leftX) / (tile.pixelGameSize ?? 1);
  const py = tileY * tileSize + (z - topZ) / (tile.pixelGameSize ?? 1);
  return [px, py];
}

function nativePixelToGame(px: number, py: number, tile: WorldTileConfig): [number, number] {
  const tileSize = tile.tileSize ?? 256;
  const [leftX, topZ] = tile.gameTopLeft ?? [0, 0];
  const [tileX, tileY] = tile.tileTopLeft ?? [0, 0];
  return [
    leftX + (px - tileX * tileSize) * (tile.pixelGameSize ?? 1),
    topZ + (py - tileY * tileSize) * (tile.pixelGameSize ?? 1),
  ];
}

function pixelToLngLat(px: number, py: number, zoom: number, tileSize: number): [number, number] {
  const worldSize = tileSize * 2 ** zoom;
  const lng = (px / worldSize) * 360 - 180;
  const y = Math.PI * (1 - (2 * py) / worldSize);
  const lat = (Math.atan(Math.sinh(y)) * 180) / Math.PI;
  return [lng, clamp(lat, -MAX_MERCATOR_LAT, MAX_MERCATOR_LAT)];
}

function lngLatToPixel(lng: number, lat: number, zoom: number, tileSize: number): [number, number] {
  const worldSize = tileSize * 2 ** zoom;
  const clampedLat = clamp(lat, -MAX_MERCATOR_LAT, MAX_MERCATOR_LAT);
  const latRad = (clampedLat * Math.PI) / 180;
  const px = ((lng + 180) / 360) * worldSize;
  const py = ((1 - Math.log(Math.tan(latRad) + 1 / Math.cos(latRad)) / Math.PI) / 2) * worldSize;
  return [px, py];
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}
