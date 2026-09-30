package ui

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type searchCursorFrame struct{ view tea.View }

func (searchCursorFrame) Init() tea.Cmd                         { return tea.Quit }
func (m searchCursorFrame) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m searchCursorFrame) View() tea.View                      { return m.view }

func TestSearchFrameRequestsNativeBlinkingBlock(t *testing.T) {
	h := start(t, newFake())
	h.press("/")
	var out bytes.Buffer
	program := tea.NewProgram(searchCursorFrame{view: h.m.View()},
		tea.WithInput(nil), tea.WithOutput(&out), tea.WithWindowSize(120, 30),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}), tea.WithoutSignalHandler())
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[1 q") {
		t.Fatalf("native renderer never requested a blinking block: %q", out.String())
	}
}

func TestFocusedSearchExposesNativeCursor(t *testing.T) {
	h := start(t, newFake())
	h.press("/")
	view := h.m.View()
	if view.Cursor == nil {
		t.Fatal("focused search has no native cursor; input methods cannot locate the field")
	}
	if got := view.Cursor.Position; got != (tea.Position{X: 4, Y: 4}) {
		t.Errorf("cursor = %+v, want column 4 on row 4", got)
	}
	if view.Cursor.Shape != tea.CursorBlock || !view.Cursor.Blink || view.Cursor.Color != nil {
		t.Errorf("cursor = %+v, want the terminal's default blinking block", view.Cursor)
	}
}

func TestSearchCursorUsesVisibleCellsInBothTabs(t *testing.T) {
	for _, tab := range []tab{tabInstalled, tabBrowse} {
		for _, tt := range []struct {
			name, value string
			at, column  int
		}{
			{name: "ascii end", value: "abc", at: 3, column: 7},
			{name: "ascii middle", value: "abc", at: 1, column: 5},
			{name: "wide end", value: "中文", at: 2, column: 8},
			{name: "wide middle", value: "中a文", at: 1, column: 6},
			{name: "combining mark", value: "e\u0301x", at: 2, column: 5},
		} {
			t.Run(tabNames[tab]+"/"+tt.name, func(t *testing.T) {
				h := start(t, newFake())
				h.m.tab = tab
				h.press("/")
				h.m.filters[tab].SetValue(tt.value)
				h.m.filters[tab].SetCursor(tt.at)
				view := h.m.View()
				if view.Cursor == nil || view.Cursor.Position != (tea.Position{X: tt.column, Y: 4}) {
					t.Fatalf("cursor = %+v, want column %d on row 4", view.Cursor, tt.column)
				}
				line := ansi.Strip(strings.Split(view.Content, "\n")[4])
				if !strings.HasPrefix(line, "  / "+tt.value) {
					t.Errorf("search value is not continuous: %q", line)
				}
				if h.m.filters[tab].VirtualCursor() {
					t.Error("the virtual cursor is still enabled beside the real cursor")
				}
			})
		}
	}
}

func TestSearchCursorTracksScrollingAndResize(t *testing.T) {
	h := start(t, newFake())
	h.press("/")
	h.m.filters[tabInstalled].SetValue("0123456789ABCDEF")
	h.m.Update(tea.WindowSizeMsg{Width: 22, Height: 30})
	assertFilter := func(prefix string, column int) {
		t.Helper()
		view := h.m.View()
		if view.Cursor == nil || view.Cursor.Position != (tea.Position{X: column, Y: 4}) {
			t.Fatalf("cursor = %+v, want column %d on row 4", view.Cursor, column)
		}
		line := ansi.Strip(strings.Split(view.Content, "\n")[4])
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "0 of 2 ") {
			t.Fatalf("filter = %q, want prefix %q and the count at the right", line, prefix)
		}
		if got := ansi.StringWidth(line); got != h.m.width {
			t.Fatalf("filter width = %d, want %d", got, h.m.width)
		}
	}
	assertFilter("  / 789ABCDEF", 13)
	h.m.filters[tabInstalled].SetCursor(8)
	assertFilter("  / 012345678", 12)
	h.m.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	assertFilter("  / 0123456789ABCDEF", 12)
	h.m.filters[tabInstalled].CursorEnd()
	assertFilter("  / 0123456789ABCDEF", 20)
	h.m.Update(tea.WindowSizeMsg{Width: 22, Height: 30})
	assertFilter("  / 789ABCDEF", 13)
}

func TestSearchCursorTracksWideScrolledText(t *testing.T) {
	h := start(t, newFake())
	h.press("/")
	h.m.filters[tabInstalled].SetValue("一二三四五六七八九十")
	h.m.Update(tea.WindowSizeMsg{Width: 22, Height: 30})
	view := h.m.View()
	if view.Cursor == nil || view.Cursor.Position != (tea.Position{X: 12, Y: 4}) {
		t.Fatalf("cursor = %+v, want column 12 on row 4", view.Cursor)
	}
	line := ansi.Strip(strings.Split(view.Content, "\n")[4])
	if !strings.HasPrefix(line, "  / 七八九十") {
		t.Fatalf("wide scrolled filter = %q", line)
	}
}

func TestSearchCursorHiddenOutsideVisibleEditableField(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*model)
	}{
		{name: "blurred", change: func(m *model) { m.filters[m.tab].Blur() }},
		{name: "confirmation", change: func(m *model) { m.confirm = &confirm{prompt: "Continue?"} }},
		{name: "other screen", change: func(m *model) { m.screen = screenOutput }},
		{name: "narrow", change: func(m *model) { m.width = 8 }},
		{name: "short", change: func(m *model) { m.height = 4 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := start(t, newFake())
			h.press("/")
			tt.change(h.m)
			if cursor := h.m.View().Cursor; cursor != nil {
				t.Fatalf("inactive or clipped field exposes cursor %+v", cursor)
			}
		})
	}
}

func TestNativeSearchKeepsEditingAndPasteBehavior(t *testing.T) {
	h := start(t, newFake())
	h.press("/")
	h.m.Update(tea.PasteMsg{Content: "中ab"})
	h.m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	h.press("x")
	if got := h.m.filters[tabInstalled].Value(); got != "中axb" {
		t.Fatalf("edited value = %q, want 中axb", got)
	}
	if cursor := h.m.View().Cursor; cursor == nil || cursor.X != 8 {
		t.Fatalf("cursor after paste and middle edit = %+v, want column 8", cursor)
	}
	h.press("tab")
	if h.m.View().Cursor != nil || h.m.filters[tabInstalled].Value() != "中axb" {
		t.Fatal("leaving search did not hide the cursor and preserve the query")
	}
	h.press("/")
	if h.m.View().Cursor == nil {
		t.Fatal("returning to search did not restore the native cursor")
	}
}
