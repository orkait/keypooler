package api

const (
	defaultWindowSeconds  = 60
	defaultUsageListLimit = 100
	maxUsageListLimit     = 1000
	exhaustedEvent        = "exhausted"
	adminConsumerID       = "admin"
	consumerTokenBytes    = 32
	maxBodySize           = 1 << 20

	pathID       = "id"
	pathName     = "name"
	featureParam = "feature"
	limitParam   = "limit"
	budgetField  = "budget"

	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	bearerScheme        = "Bearer"
	mediaJSON           = "application/json"

	statusOK        = "ok"
	statusDeleted   = "deleted"
	statusExhausted = "exhausted"

	msgInvalidJSON   = "invalid JSON: "
	msgDatabaseError = "database error"
	msgTierNotFound  = "tier not found: "
)
