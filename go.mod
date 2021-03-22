module github.com/diamondburned/tcell-sixel

go 1.16

// replace with intercept-ifaces branch
replace github.com/gdamore/tcell/v2 => github.com/diamondburned/tcell/v2 v2.0.0-20210315050139-52ab8c9d235a

require (
	github.com/ericpauley/go-quantize v0.0.0-20200331213906-ae555eb2afa4
	github.com/gdamore/tcell/v2 v2.2.0
	github.com/mattn/go-sixel v0.0.2-0.20210304070930-abc463a8f9c4
	github.com/pkg/errors v0.9.1
	golang.org/x/image v0.0.0-20210220032944-ac19c3e999fb
)
