// Command genicon renders the fnOS object storage app icon without any
// external dependency, so the package can be rebuilt offline.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const ss = 4 // supersampling factor

func main() {
	targets := []struct {
		size int
		path string
	}{
		{64, "pack/ICON.PNG"},
		{256, "pack/ICON_256.PNG"},
		{64, "pack/app/ui/images/icon_64.png"},
		{256, "pack/app/ui/images/icon_256.png"},
	}
	for _, t := range targets {
		if err := writeIcon(t.path, t.size); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", t.path, err)
			os.Exit(1)
		}
		fmt.Println("wrote", t.path)
	}
}

func writeIcon(path string, size int) error {
	img := render(size)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func render(size int) *image.NRGBA {
	w := size * ss
	buf := image.NewNRGBA(image.Rect(0, 0, w, w))
	scale := float64(w) / 64.0 // design space is 64x64

	// Flat rounded square in deep slate. No gradient: the mark has to stay
	// readable at 64px and avoid the generic cloud-vendor cube look.
	margin := 2.0
	radius := 14.0
	bg := color.NRGBA{R: 0x1b, G: 0x24, B: 0x32, A: 0xff}
	brand := color.NRGBA{R: 0x34, G: 0x68, B: 0xf5, A: 0xff}
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			px := (float64(x) + 0.5) / scale
			py := (float64(y) + 0.5) / scale
			if roundedRectSDF(px, py, margin, margin, 64-2*margin, 64-2*margin, radius) <= 0 {
				buf.SetNRGBA(x, y, bg)
			}
		}
	}

	// Ring with a rounded object block notched into its lower right, matching
	// the favicon and the console brand mark.
	const cx, cy, r, sw = 31.0, 31.0, 14.0, 6.0
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			px := (float64(x) + 0.5) / scale
			py := (float64(y) + 0.5) / scale
			d := math.Hypot(px-cx, py-cy)
			if edge := sw/2 - math.Abs(d-r); edge > 0 {
				blend(buf, x, y, white, clamp01(edge*scale)) // 1px feather
			}
			// Punch the notch out of the ring so the block reads as a separate object.
			if roundedRectSDF(px, py, 32.25, 32.25, 16.5, 16.5, 5.5) <= 0 {
				buf.SetNRGBA(x, y, bg)
			}
			if roundedRectSDF(px, py, 34, 34, 13, 13, 4) <= 0 {
				blend(buf, x, y, brand, 1)
			}
		}
	}

	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for dy := 0; dy < ss; dy++ {
				for dx := 0; dx < ss; dx++ {
					c := buf.NRGBAAt(x*ss+dx, y*ss+dy)
					af := float64(c.A) / 255
					r += float64(c.R) * af
					g += float64(c.G) * af
					b += float64(c.B) * af
					a += af
				}
			}
			n := float64(ss * ss)
			if a == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{
				R: clamp255(r / a),
				G: clamp255(g / a),
				B: clamp255(b / a),
				A: uint8(clamp01(a/n)*255 + 0.5),
			})
		}
	}
	return out
}

func roundedRectSDF(px, py, x, y, w, h, r float64) float64 {
	cx, cy := x+w/2, y+h/2
	qx := math.Abs(px-cx) - (w/2 - r)
	qy := math.Abs(py-cy) - (h/2 - r)
	ax, ay := math.Max(qx, 0), math.Max(qy, 0)
	return math.Hypot(ax, ay) + math.Min(math.Max(qx, qy), 0) - r
}

func blend(img *image.NRGBA, x, y int, c color.NRGBA, a float64) {
	if a <= 0 {
		return
	}
	dst := img.NRGBAAt(x, y)
	da := float64(dst.A) / 255
	sa := a
	oa := sa + da*(1-sa)
	if oa <= 0 {
		img.SetNRGBA(x, y, color.NRGBA{})
		return
	}
	mix := func(s, d uint8) uint8 {
		return uint8((float64(s)*sa + float64(d)*da*(1-sa)) / oa)
	}
	img.SetNRGBA(x, y, color.NRGBA{R: mix(c.R, dst.R), G: mix(c.G, dst.G), B: mix(c.B, dst.B), A: uint8(oa*255 + 0.5)})
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// clamp255 converts a premultiplied average back into an 8-bit channel value.
func clamp255(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
