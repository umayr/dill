package config

// ImageReferences collects the complete declared image set, including hook jobs.
func ImageReferences(cfg *DillConfig) (map[string]string, map[string]string) {
	s, j := map[string]string{}, map[string]string{}
	for k, v := range cfg.Services {
		s[k] = v.Image
	}
	for k, v := range cfg.Jobs {
		j[k] = v.Image
	}
	return s, j
}
