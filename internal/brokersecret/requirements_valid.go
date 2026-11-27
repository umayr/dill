package brokersecret

// Valid checks untrusted, value-free discovery metadata; it grants no execution.
func (r Requirements) Valid() bool {
	if r.Contract != Version || !nameOK(r.Stack) || (r.Engine != "docker" && r.Engine != "podman") || !validDigest(r.ConfigDigest) || r.Names == nil || r.Grants == nil || len(r.Names) > MaxEntries {
		return false
	}
	names := map[string]bool{}
	for _, name := range r.Names {
		if !nameOK(name) || names[name] {
			return false
		}
		names[name] = true
	}
	if len(names) == 0 {
		return len(r.Grants) == 0
	}
	return validateGrants(names, r.Grants) == nil
}
