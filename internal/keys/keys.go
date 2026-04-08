package keys

const (
	SeenBloomFilter   = "vortex:frontier:seen"
	CooldownQueue     = "vortex:frontier:cooldown"
	RobotsCachePrefix = "vortex:frontier:robots:"
	RateLimitPrefix   = "vortex:frontier:limit:"

	DeadLetterExchange    = "vortex.dlx"
	FrontierQueue         = "vortex.frontier.pending"
	FrontierDLQ           = "vortex.frontier.dlq"
	FrontierDLQRoutingKey = "frontier.dead"

	ProcessingQueue         = "vortex.processing.pending"
	ProcessingDLQ           = "vortex.processing.dlq"
	ProcessingDLQRoutingKey = "processing.dead"
)
