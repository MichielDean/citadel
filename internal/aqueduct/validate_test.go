package aqueduct

// minimalValidConfig returns the smallest valid AqueductConfig for use as a
// base in table-driven tests.
func minimalValidConfig() AqueductConfig {
	return AqueductConfig{
		Repos: []RepoConfig{
			{Name: "test-repo", Cataractae: 1, Prefix: "t"},
		},
	}
}
