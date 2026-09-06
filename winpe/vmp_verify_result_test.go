package winpe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVMPVerifyOutput_Success(t *testing.T) {
	output := VMPVerifyBanner + "\n"
	for _, svc := range VMPTransplantServices() {
		output += svc.Name + "_SC=RUNNING\n"
		output += svc.Name + "_START=0\n"
	}
	output += VMPVerifyComplete + "\n"

	r := ParseVMPVerifyOutput(output)
	assert.True(t, r.Started)
	assert.True(t, r.Complete)
	assert.Equal(t, "RUNNING", r.Services["hvservice"].SCState)
	assert.Equal(t, "0", r.Services["hvservice"].StartVal)
	assert.Equal(t, "0", r.Services["vmbus"].StartVal)
	assert.NoError(t, r.Validate())
}

func TestParseVMPVerifyOutput_NotStarted(t *testing.T) {
	r := ParseVMPVerifyOutput("some random output")
	assert.False(t, r.Started)
	assert.False(t, r.Complete)
	require.Error(t, r.Validate())
	assert.Contains(t, r.Validate().Error(), "did not start")
}

func TestParseVMPVerifyOutput_Incomplete(t *testing.T) {
	output := VMPVerifyBanner + "\n" +
		"vmms_SC=RUNNING\n"

	r := ParseVMPVerifyOutput(output)
	assert.True(t, r.Started)
	assert.False(t, r.Complete)
	require.Error(t, r.Validate())
	assert.Contains(t, r.Validate().Error(), "did not complete")
}

func TestParseVMPVerifyOutput_ServiceNotExist(t *testing.T) {
	output := VMPVerifyBanner + "\n" +
		"hvservice_SC=NOT_EXIST\n" +
		"hvservice_START=ABSENT\n" +
		VMPVerifyComplete + "\n"

	r := ParseVMPVerifyOutput(output)
	assert.True(t, r.Complete)
	require.Error(t, r.Validate())
	assert.Contains(t, r.Validate().Error(), "hvservice")
	assert.Contains(t, r.Validate().Error(), "does not recognise")
}

func TestVMPVerifyResult_ServicesMatchTransplantManifest(t *testing.T) {
	services := VMPTransplantServices()
	require.NotEmpty(t, services, "VMPTransplantServices must return at least one service")

	output := VMPVerifyBanner + "\n"
	for _, svc := range services {
		output += svc.Name + "_SC=RUNNING\n"
		output += svc.Name + "_START=0\n"
	}
	output += VMPVerifyComplete + "\n"

	r := ParseVMPVerifyOutput(output)
	assert.NoError(t, r.Validate())
	assert.Equal(t, len(services), len(r.Services))
}
