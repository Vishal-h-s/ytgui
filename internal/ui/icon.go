package ui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed assets/app_icon.svg
var appIconSVG []byte

var appIcon = fyne.NewStaticResource("app_icon.svg", appIconSVG)
