package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// accentColor is the one spot of color the UI leans on for emphasis
// (selected items, cursors, active state). It's a standard ANSI code, not a
// truecolor hex value, so terminal colorschemes (gruvbox, solarized, etc.)
// remap it along with everything else instead of it fighting the theme.
var accentColor = lipgloss.Color("12")

// newListStyles builds the conversation list's chrome (title bar, status
// bar, pagination) out of terminal attributes and accentColor rather than
// bubbles' default truecolor palette, so it reads correctly against any
// background the user's terminal theme sets.
func newListStyles() list.Styles {
	s := list.DefaultStyles()

	s.TitleBar = lipgloss.NewStyle().Padding(0, 0, 1, 2)
	s.Title = lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)

	s.Spinner = lipgloss.NewStyle().Foreground(accentColor)
	s.FilterPrompt = lipgloss.NewStyle().Foreground(accentColor)
	s.FilterCursor = lipgloss.NewStyle().Foreground(accentColor)
	s.DefaultFilterCharacterMatch = lipgloss.NewStyle().Underline(true)

	s.StatusBar = lipgloss.NewStyle().Faint(true).Padding(0, 0, 1, 2)
	s.StatusEmpty = lipgloss.NewStyle().Faint(true)
	s.StatusBarActiveFilter = lipgloss.NewStyle()
	s.StatusBarFilterCount = lipgloss.NewStyle().Faint(true)

	s.NoItems = lipgloss.NewStyle().Faint(true)

	s.PaginationStyle = lipgloss.NewStyle().PaddingLeft(2)
	s.HelpStyle = lipgloss.NewStyle().Padding(1, 0, 0, 2)

	s.ActivePaginationDot = lipgloss.NewStyle().Foreground(accentColor).SetString("•")
	s.InactivePaginationDot = lipgloss.NewStyle().Faint(true).SetString("•")
	s.ArabicPagination = lipgloss.NewStyle().Faint(true)
	s.DividerDot = lipgloss.NewStyle().Faint(true).SetString(" • ")

	return s
}

// newItemStyles mirrors list.NewDefaultItemStyles but swaps its hardcoded
// pink/purple accents and light/dark gray hexes for accentColor and Faint,
// which adapt to the terminal's own foreground and background.
func newItemStyles() list.DefaultItemStyles {
	var s list.DefaultItemStyles

	s.NormalTitle = lipgloss.NewStyle().Padding(0, 0, 0, 2)
	s.NormalDesc = s.NormalTitle.Faint(true)

	s.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(accentColor).
		Foreground(accentColor).
		Bold(true).
		Padding(0, 0, 0, 1)
	s.SelectedDesc = s.SelectedTitle.Foreground(accentColor).Bold(false)

	s.DimmedTitle = lipgloss.NewStyle().Faint(true).Padding(0, 0, 0, 2)
	s.DimmedDesc = s.DimmedTitle

	s.FilterMatch = lipgloss.NewStyle().Underline(true).Bold(true)

	return s
}
