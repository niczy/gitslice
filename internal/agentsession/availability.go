package agentsession

const (
	SessionAvailabilityLocal         = "local"
	SessionAvailabilityPendingLocal  = "pending_local"
	SessionAvailabilityCloudOnly     = "cloud_only"
	SessionAvailabilityRunnerOffline = "runner_offline"
	SessionAvailabilityFailed        = "failed"
	SessionAvailabilityUnknown       = "unknown"

	RunnerCapabilityLocalSessionIDs       = "local_session_ids"
	RunnerCapabilityLocalSessionsReported = "local_sessions_reported"
)
