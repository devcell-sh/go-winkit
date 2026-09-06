package mctcatalog

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseBuildFromFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "arm64 professional",
			filename: "26100.4349.250607-1500_arm64fre_CLIENT_PROFESSIONAL_OEM_A64FRE_en-us.esd",
			want:     "26100.4349",
		},
		{
			name:     "x64 consumer",
			filename: "26100.4349.250607-1500_x64fre_CLIENT_CONSUMER_OEM_X64FRE_en-us.esd",
			want:     "26100.4349",
		},
		{
			name:     "different build",
			filename: "22621.1.220506-1250_arm64fre_CLIENT_PROFESSIONAL_OEM_A64FRE_en-us.esd",
			want:     "22621.1",
		},
		{
			name:     "no match",
			filename: "random-file.esd",
			want:     "",
		},
		{
			name:     "empty",
			filename: "",
			want:     "",
		},
		{
			name:     "series only prefix",
			filename: "26100.250607-1500_arm64fre.esd",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBuildFromFilename(tt.filename)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestESDFile_BuildNumber(t *testing.T) {
	e := ESDFile{
		FileName: "26100.4349.250607-1500_arm64fre_CLIENT_PROFESSIONAL_OEM_A64FRE_en-us.esd",
	}
	assert.Equal(t, "26100.4349", e.BuildNumber())
}
