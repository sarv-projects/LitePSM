package secrets

// OpenSecretStore opens the platform-appropriate secure credential store.
func OpenSecretStore() (SecretStore, error) {
	return newPlatformSecretStore()
}
