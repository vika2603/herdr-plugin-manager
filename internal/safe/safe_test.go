package safe

import "testing"

// esc builds the escaped form Line prints, so the expectations below are
// visibly backslash sequences rather than the characters they stand for.
func esc(seq string) string { return `\` + seq }

func TestLine(t *testing.T) {
	csi := string(rune(0x9b))
	rlo := string(rune(0x202e))
	tests := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"Terminal Browser · ★ 3486", "Terminal Browser · ★ 3486"},
		{"x\x1b[6A\x1b[J  Build commands", "x" + esc("x1b") + "[6A" + esc("x1b") + "[J  Build commands"},
		{"sh evil.sh\r    make build", "sh evil.sh     make build"},
		{"two\nlines\tand tab", "two lines and tab"},
		{"safe" + rlo + "hs.live", "safe" + esc("u202e") + "hs.live"},
		{csi + "31m", esc("u009b") + "31m"},
		{"bad \xff byte", "bad " + esc("ufffd") + " byte"},
	}
	for _, tt := range tests {
		if got := Line(tt.in); got != tt.want {
			t.Errorf("Line(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTextKeepsLines(t *testing.T) {
	in := "Cloning\r\nbuilding\x1b[2K\n\tdone\roverwritten"
	want := "Cloning\nbuilding" + esc("x1b") + "[2K\n\tdone" + esc("r") + "overwritten"
	if got := Text(in); got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
}
