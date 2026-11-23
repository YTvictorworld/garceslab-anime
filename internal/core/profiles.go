package core

// Garceslab Anime: local profiles wiring (see internal/profiles/HOOKS.md).

import (
	"os"
	"seanime/internal/profiles"

	"github.com/rs/zerolog"
)

// initProfiles opens profiles.db. Startup fails if it can't be opened, if foreign
// keys aren't enforced or if a migration fails: running without the profile store
// would silently mix user data.
func initProfiles(cfg *Config, logger *zerolog.Logger) *profiles.Service {
	dataDir := cfg.Data.AppDataDir
	if os.Getenv("TEST_ENV") == "true" {
		dataDir = "" // in-memory, like seanime.db in tests
	}
	svc, err := profiles.Open(profiles.Options{
		DataDir: dataDir,
		Enforce: profiles.EnforceFromEnv(),
		Logger:  logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("app: Failed to initialize profiles database")
	}
	svc.StartPurgeLoop()
	logger.Info().Bool("enforce", svc.Enforced()).Msg("profiles: Profiles database ready")
	return svc
}
