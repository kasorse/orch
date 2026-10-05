package execution

// resetCredentialVarsForTest empties the credential set; register it with t.Cleanup in
// any test that calls ScrubCredentials.
func resetCredentialVarsForTest() {
	credentialMu.Lock()
	defer credentialMu.Unlock()
	credentialVars = map[string]bool{}
}

func resetTestEnvPassForTest() {
	testEnvMu.Lock()
	defer testEnvMu.Unlock()
	testEnvPass = map[string]bool{}
}
