package winpe

// Feature is one capability-probe result within a FeatureGroup.
type Feature struct {
	Name    string
	Detail  string
	Present bool
}

// FeatureGroup is a labelled set of related capability probes over a WinPE
// boot.wim image, as reported by InspectFeatures.
type FeatureGroup struct {
	Name  string
	Items []Feature
}
