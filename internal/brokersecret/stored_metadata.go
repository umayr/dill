package brokersecret

import "time"

type storedFile struct {
	Grant Grant  `json:"grant"`
	File  string `json:"file"`
}
type manifest struct {
	Contract   string       `json:"contract"`
	Review     Review       `json:"review"`
	CreatedAt  time.Time    `json:"createdAt"`
	Files      []storedFile `json:"files"`
	MAC        string       `json:"mac"`
	Retirement *Retirement  `json:"retirement,omitempty"`
}

// Old ordinary manifests stay byte-compatible under JSON/MAC. A distinct
// retirement contract is required for zero-file metadata; this is not MAC proof.
func validManifestKind(m manifest) bool {
	if m.Contract == Version {
		return m.Retirement == nil && len(m.Files) > 0
	}
	return m.Contract == RetirementVersion && len(m.Files) == 0 && m.Retirement != nil && m.Retirement.Valid() && m.Retirement.Scope == m.Review.Scope
}
