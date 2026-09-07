package isokit

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-winkit/internal/iotrace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Image creation is the largest opaque step in a build, so a trace has to
// name the artifact and its resulting size, not merely that it happened.
func TestCreateFATImage_TracesTheImageAndItsSize(t *testing.T) {
	var buf bytes.Buffer
	restore := iotrace.SetDefault(iotrace.NewLogger(&buf))
	defer restore()

	imgPath := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, CreateFATImage(imgPath, map[string][]byte{
		"/startup.nsh": []byte("echo hi\n"),
	}))

	out := buf.String()
	assert.Contains(t, out, iotrace.KindCreateFAT)
	assert.Contains(t, out, imgPath)
	assert.Contains(t, out, "1 file(s)", "the trace must say how much went in")
	assert.Contains(t, out, "MB", "and how big the image came out")
}

// A failed creation is the case tracing exists for, so the error must
// reach the trace rather than only the return value.
func TestCreateFATImage_TracesFailure(t *testing.T) {
	var buf bytes.Buffer
	restore := iotrace.SetDefault(iotrace.NewLogger(&buf))
	defer restore()

	err := CreateFATImage("/nonexistent-dir/answer.img", map[string][]byte{
		"/a": []byte("x"),
	})
	require.Error(t, err)

	assert.Contains(t, buf.String(), "ERR", "the failure must appear in the trace")
}

func TestReadFileFromISO_TracesTheMemberPath(t *testing.T) {
	isoPath := filepath.Join(t.TempDir(), "t.iso")
	require.NoError(t, CreateSimpleISO(isoPath, map[string][]byte{
		"/hello.txt": []byte("world"),
	}))

	var buf bytes.Buffer
	restore := iotrace.SetDefault(iotrace.NewLogger(&buf))
	defer restore()

	data, err := ReadFileFromISO(isoPath, "/hello.txt")
	require.NoError(t, err)
	require.Equal(t, []byte("world"), data)

	out := buf.String()
	assert.Contains(t, out, isoPath)
	assert.Contains(t, out, "hello.txt",
		"which member was read is the whole point of tracing a read")
}

// Tracing is on every IO call site, so the disabled path must stay silent
// and must not change behaviour.
func TestTracing_IsSilentWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	restore := iotrace.SetDefault(iotrace.NewLogger(&buf))
	restore() // back to the Nop default

	imgPath := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, CreateFATImage(imgPath, map[string][]byte{"/a": []byte("x")}))

	assert.Empty(t, buf.String(), "no output once tracing is restored to off")
}
