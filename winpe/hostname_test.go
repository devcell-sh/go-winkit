package winpe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGuestHostname_IsTheCellID(t *testing.T) {
	require.Equal(t, "main", GuestHostname("main"))
	require.Equal(t, "DIMM", GuestHostname("DIMM"))
}

func TestGuestHostname_TruncatesToTheNetBIOSLimit(t *testing.T) {
	got := GuestHostname("a-very-long-cell-name-indeed")

	require.LessOrEqual(t, len(got), 15, "NetBIOS names are capped at 15 characters")
	require.Equal(t, "a-very-long-cel", got)
}

func TestGuestHostname_ReplacesCharactersNetBIOSForbids(t *testing.T) {
	got := GuestHostname(`my cell:name*?`)

	require.NotContains(t, got, " ")
	for _, bad := range []string{`\`, `/`, `:`, `*`, `?`, `"`, `<`, `>`, `|`} {
		require.NotContains(t, got, bad, "forbidden character %q survived", bad)
	}
	require.Equal(t, "my-cell-name", got)
}

func TestGuestHostname_FallsBackWhenNothingUsableRemains(t *testing.T) {
	for _, in := range []string{"", "   ", `\/:*`} {
		got := GuestHostname(in)
		require.NotEmpty(t, got, "input %q produced an empty hostname", in)
		require.LessOrEqual(t, len(got), 15)
		require.False(t, strings.HasPrefix(got, "-"), "a hostname may not start with a separator")
	}
}
