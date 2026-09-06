package winpe

import (
	"fmt"
	"strings"
)

// VMPVerifyResult holds the parsed output of the VMP verify script.
type VMPVerifyResult struct {
	Started  bool
	Complete bool
	Services map[string]VMPServiceStatus
	Raw      string
}

// VMPServiceStatus holds the state of a single VMP service as reported
// by the verify script.
type VMPServiceStatus struct {
	SCState  string // RUNNING, STOPPED, NOT_EXIST, UNKNOWN
	StartVal string // 0, 1, 2, 3, 4, or ABSENT
}

// ParseVMPVerifyOutput parses the KEY=VALUE output of the VMP verify script.
func ParseVMPVerifyOutput(output string) VMPVerifyResult {
	r := VMPVerifyResult{
		Started:  strings.Contains(output, VMPVerifyBanner),
		Complete: strings.Contains(output, VMPVerifyComplete),
		Services: make(map[string]VMPServiceStatus),
		Raw:      output,
	}

	for _, svc := range VMPTransplantServices() {
		status := VMPServiceStatus{}
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, svc.Name+"_SC=") {
				status.SCState = strings.TrimPrefix(line, svc.Name+"_SC=")
			}
			if strings.HasPrefix(line, svc.Name+"_START=") {
				status.StartVal = strings.TrimPrefix(line, svc.Name+"_START=")
			}
		}
		r.Services[svc.Name] = status
	}

	return r
}

// Validate checks that the VMP verify completed and all services are
// registered. Returns nil on success, or an error listing problems.
func (r VMPVerifyResult) Validate() error {
	if !r.Started {
		return fmt.Errorf("VMP verify script did not start (missing banner)")
	}
	if !r.Complete {
		return fmt.Errorf("VMP verify script did not complete")
	}

	var problems []string
	for name, s := range r.Services {
		if s.SCState == "NOT_EXIST" {
			problems = append(problems, fmt.Sprintf("%s: SCM does not recognise it", name))
		}
		if s.StartVal == "ABSENT" {
			problems = append(problems, fmt.Sprintf("%s: no Start value", name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("VMP services not fully registered:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
