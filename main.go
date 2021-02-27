package main

import (
	"log"

	_ "image/jpeg"
	_ "image/png"

	"github.com/gdamore/tcell/v2"
)

func main() {
	img, err := readImage("/home/diamond/Pictures/astolfo_ava_n.png", 250)
	if err != nil {
		log.Fatalln("failed to read image:", err)
	}

	container := NewSIXELContainer(img)

	screen, err := tcell.NewScreen()
	if err != nil {
		log.Fatalln("failed to create screen:", err)
	}

	iceptAdder, ok := screen.(tcell.DrawInterceptAdder)
	if !ok {
		log.Fatalln("screen does not take draw interceptors")
	}

	// Draw offsets.
	const (
		offsetX = 0
		offsetY = 1
	)

	iceptAdder.AddDrawInterceptAfter(func(screen tcell.Screen, sync bool) {
		szpx, _ := screen.(tcell.PixelSizer)

		// Get the terminal dimensions.
		w, h := screen.Size()
		xpx, ypx := szpx.PixelSize()
		// If any of these information are uninitialized, then don't bother.
		if w == 0 || h == 0 || xpx == 0 || ypx == 0 {
			return
		}

		cellW, cellH := cellSz(w, h, xpx, ypx)

		// Update the upper bounds. We recalculate the pixel size here to round
		// down the actual size.
		container.Bounds.Max.X = cellW * (w - 1)
		container.Bounds.Max.Y = cellH * (h - 1)

		container.Bounds.Min.X = cellW * (offsetX + 1)
		container.Bounds.Min.Y = cellH * (offsetY + 1)

		// Draw the sixel on L1.
		screen.ShowCursor(offsetX, offsetY)
		defer screen.HideCursor()

		drawer, _ := screen.(tcell.DirectDrawer)
		drawer.DrawDirectly(container.Encode())
	})

	if err := screen.Init(); err != nil {
		log.Fatalln("failed to init screen:", err)
	}
	defer screen.Fini()

	screen.SetCell(
		0, 0, tcell.StyleDefault,
		[]rune("Hello, world! Look at this SIXEL:")...,
	)

	for {
		switch ev := screen.PollEvent().(type) {
		case *tcell.EventKey:
			// Exit on Esc.
			if ev.Key() == tcell.KeyEscape {
				return
			}
		}
	}
}
