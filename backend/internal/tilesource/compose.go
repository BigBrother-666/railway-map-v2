package tilesource

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// composeTiles 把下一级 2×2 张子瓦片（顺序：左上、右上、左下、右下）拼接并缩小为一张 size×size 的瓦片，
// 用于合成上游最粗一级之上的层级（MapLibre 不会把低于 source minzoom 的瓦片缩小显示）。
// 四张都是空瓦片时返回空瓦片；四张都是 JPEG 时编码为 JPEG，否则为 PNG（保留空白区域的透明）。
func composeTiles(children [4]Tile, size, jpegQuality int) (Tile, error) {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	half := size / 2
	allJPEG, drawn := true, false
	for i, c := range children {
		if isBlank(c) {
			allJPEG = false
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(c.Data))
		if err != nil {
			return Tile{}, fmt.Errorf("解码子瓦片失败（%s）: %w", c.ContentType, err)
		}
		if c.ContentType != "image/jpeg" {
			allJPEG = false
		}
		x0, y0 := i%2*half, i/2*half
		downscaleInto(dst, image.Rect(x0, y0, x0+half, y0+half), img)
		drawn = true
	}
	if !drawn {
		return blankTile(), nil
	}
	var buf bytes.Buffer
	if allJPEG {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return Tile{}, err
		}
		return Tile{Data: buf.Bytes(), ContentType: "image/jpeg"}, nil
	}
	if err := png.Encode(&buf, dst); err != nil {
		return Tile{}, err
	}
	return Tile{Data: buf.Bytes(), ContentType: "image/png"}, nil
}

// downscaleInto 把 src 按区域平均缩放到 dst 的 r 区域（预乘 alpha 下取平均，透明边缘不发黑）。
func downscaleInto(dst *image.RGBA, r image.Rectangle, src image.Image) {
	b := src.Bounds()
	sw, sh, w, h := b.Dx(), b.Dy(), r.Dx(), r.Dy()
	for j := 0; j < h; j++ {
		sy0, sy1 := b.Min.Y+j*sh/h, b.Min.Y+max((j+1)*sh/h, j*sh/h+1)
		for i := 0; i < w; i++ {
			sx0, sx1 := b.Min.X+i*sw/w, b.Min.X+max((i+1)*sw/w, i*sw/w+1)
			var sr, sg, sb, sa, n uint32
			for y := sy0; y < sy1; y++ {
				for x := sx0; x < sx1; x++ {
					cr, cg, cb, ca := src.At(x, y).RGBA()
					sr, sg, sb, sa, n = sr+cr, sg+cg, sb+cb, sa+ca, n+1
				}
			}
			dst.SetRGBA(r.Min.X+i, r.Min.Y+j, color.RGBA{
				R: uint8(sr / n >> 8), G: uint8(sg / n >> 8), B: uint8(sb / n >> 8), A: uint8(sa / n >> 8),
			})
		}
	}
}

func isBlank(t Tile) bool {
	return bytes.Equal(t.Data, transparentPNG)
}
