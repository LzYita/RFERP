package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// customTheme 企业蓝主题
type customTheme struct{}

func newCustomTheme() fyne.Theme {
	return &customTheme{}
}

func NewTheme() fyne.Theme {
	return newCustomTheme()
}

// palette
var (
	// 主色
	clrPrimary      = color.RGBA{0x23, 0x6F, 0xE0, 0xFF} // 蓝
	clrPrimaryDark  = color.RGBA{0x1B, 0x5B, 0xC2, 0xFF}
	clrPrimaryLight = color.RGBA{0x64, 0x9F, 0xFF, 0xFF}
	clrFocus        = color.RGBA{0x23, 0x6F, 0xE0, 0x40}

	// 中性色
	clrBg          = color.RGBA{0xF2, 0xF4, 0xF8, 0xFF}
	clrSurface     = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	clrHover       = color.RGBA{0x23, 0x6F, 0xE0, 0x0F}
	clrBorder      = color.RGBA{0xDC, 0xE1, 0xE8, 0xFF}
	clrForeground  = color.RGBA{0x1F, 0x2A, 0x3A, 0xFF}
	clrForeground2 = color.RGBA{0x62, 0x6E, 0x7E, 0xFF}
	clrDisabled    = color.RGBA{0xBF, 0xC7, 0xD1, 0xFF}

	colorWhite = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}

	// 状态色
	clrSuccess = color.RGBA{0x2E, 0xB8, 0x72, 0xFF}
	clrWarning = color.RGBA{0xF5, 0xA9, 0x23, 0xFF}
	clrError   = color.RGBA{0xE1, 0x4B, 0x4B, 0xFF}

	// 表格
	clrHeader   = color.RGBA{0xEA, 0xEF, 0xF6, 0xFF}
	clrZebra    = color.RGBA{0xF7, 0xF9, 0xFC, 0xFF}
	clrSelected = color.RGBA{0xDA, 0xE6, 0xFB, 0xFF}
	clrWarnBg   = color.RGBA{0xFF, 0xE9, 0xE0, 0xFF}

	// 状态徽章底色
	badgeSuccessBg = color.RGBA{0xE6, 0xF7, 0xEE, 0xFF}
	badgeSuccessFg = color.RGBA{0x1E, 0x8A, 0x54, 0xFF}
	badgeWarnBg    = color.RGBA{0xFF, 0xF4, 0xDF, 0xFF}
	badgeWarnFg    = color.RGBA{0xB4, 0x70, 0x12, 0xFF}
	badgeErrorBg   = color.RGBA{0xFD, 0xE8, 0xE8, 0xFF}
	badgeErrorFg   = color.RGBA{0xC2, 0x3A, 0x3A, 0xFF}
	badgeGrayBg    = color.RGBA{0xEC, 0xEF, 0xF3, 0xFF}
	badgeGrayFg    = color.RGBA{0x5B, 0x66, 0x73, 0xFF}
	badgeBlueBg    = color.RGBA{0xE4, 0xEF, 0xFD, 0xFF}
	badgeBlueFg    = color.RGBA{0x1B, 0x5B, 0xC2, 0xFF}
)

func (t *customTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	_ = variant
	switch name {
	case theme.ColorNamePrimary:
		return clrPrimary
	case theme.ColorNameFocus:
		return clrFocus
	case theme.ColorNameHover:
		return clrHover
	case theme.ColorNameBackground:
		return clrBg
	case theme.ColorNameButton:
		return clrSurface
	case theme.ColorNameInputBackground:
		return clrSurface
	case theme.ColorNamePlaceHolder:
		return clrForeground2
	case theme.ColorNameForeground:
		return clrForeground
	case theme.ColorNameDisabled:
		return clrDisabled
	case theme.ColorNameDisabledButton:
		return clrDisabled
	case theme.ColorNameSelection:
		return clrSelected
	case theme.ColorNameSeparator:
		return clrBorder
	case theme.ColorNameSuccess:
		return clrSuccess
	case theme.ColorNameWarning:
		return clrWarning
	case theme.ColorNameError:
		return clrError
	case theme.ColorNameHyperlink:
		return clrPrimary
	case theme.ColorNameShadow:
		return color.RGBA{0x00, 0x00, 0x00, 0x30}
	case theme.ColorNameMenuBackground:
		return clrSurface
	case theme.ColorNameScrollBar:
		return clrBorder
	case theme.ColorNamePressed:
		return clrPrimaryDark
	}
	return theme.DefaultTheme().Color(name, variant)
}

func (t *customTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *customTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *customTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameText:
		return 14
	}
	return theme.DefaultTheme().Size(name)
}
