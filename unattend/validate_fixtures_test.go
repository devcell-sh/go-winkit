package unattend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateFixtures runs the generator validator (the same Validate that
// writeAnswerImage gates volume creation on) against hand-authored good/bad
// answer files. Each bad fixture violates exactly one rule and must be rejected
// with a message naming it; the good fixture must pass clean. This locks the
// validator's behavior against real answer-file shapes, not just generated ones.
func TestValidateFixtures(t *testing.T) {
	cases := []struct {
		file      string
		wantClean bool
		wantMsg   string // substring required in some error (when !wantClean)
	}{
		{"good.xml", true, ""},
		{"bad-misplaced.xml", false, "Windows Setup ignores it silently"},
		{"bad-wrong-pass.xml", false, "only valid in"},
		{"bad-banned.xml", false, "must not be used"},
		{"bad-long-command.xml", false, "limit 259"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "validate", tc.file))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}
			errs := Validate(data)

			if tc.wantClean {
				if len(errs) != 0 {
					t.Fatalf("good fixture must validate clean, got %d error(s): %v", len(errs), errs)
				}
				return
			}

			if len(errs) == 0 {
				t.Fatalf("bad fixture must be rejected, got no errors")
			}
			var matched bool
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.wantMsg) {
					matched = true
				}
			}
			if !matched {
				t.Errorf("expected an error containing %q; got: %v", tc.wantMsg, errs)
			}
		})
	}
}
