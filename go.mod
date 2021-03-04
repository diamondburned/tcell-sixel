module github.com/diamondburned/tcell-sixel

go 1.16

// replace with intercept-ifaces branch
// replace github.com/gdamore/tcell/v2 => ../tcell
replace github.com/gdamore/tcell/v2 => github.com/diamondburned/tcell/v2 v2.0.0-20210301010819-bb21ae87f80f

require (
	github.com/disintegration/imaging v1.6.2
	github.com/gdamore/tcell/v2 v2.2.0
	github.com/mattn/go-sixel v0.0.1
	github.com/pkg/errors v0.9.1
	github.com/soniakeys/quant v1.0.0 // indirect
	golang.org/x/image v0.0.0-20210220032944-ac19c3e999fb // indirect
)
