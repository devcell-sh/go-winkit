package qemu

import "testing"

// TestQcodeForRune locks the character→qcode mapping the vmkey typer relies on:
// letters lower-case with no shift, upper-case with shift, digits direct, and a
// spread of the punctuation a diagnostic PowerShell line uses (paths, quotes,
// redirection, semicolons). A wrong mapping silently types the wrong command
// into a VM we cannot see over SSH, so pin the ones that matter.
func TestQcodeForRune(t *testing.T) {
	cases := []struct {
		r       rune
		qcode   string
		shifted bool
		ok      bool
	}{
		{'a', "a", false, true},
		{'z', "z", false, true},
		{'A', "a", true, true},
		{'Z', "z", true, true},
		{'0', "0", false, true},
		{'7', "7", false, true},
		{' ', "spc", false, true},
		{'\n', "ret", false, true},
		{'\\', "backslash", false, true},
		{'/', "slash", false, true},
		{'.', "dot", false, true},
		{';', "semicolon", false, true},
		{'-', "minus", false, true},
		{':', "semicolon", true, true},
		{'"', "apostrophe", true, true},
		{'|', "backslash", true, true},
		{'*', "8", true, true},
		{'(', "9", true, true},
		{')', "0", true, true},
		{'>', "dot", true, true},
		{'?', "slash", true, true},
		{'\x00', "", false, false}, // unmappable → skipped
	}
	for _, tc := range cases {
		q, sh, ok := qcodeForRune(tc.r)
		if q != tc.qcode || sh != tc.shifted || ok != tc.ok {
			t.Errorf("qcodeForRune(%q) = (%q,%v,%v); want (%q,%v,%v)",
				tc.r, q, sh, ok, tc.qcode, tc.shifted, tc.ok)
		}
	}
}
