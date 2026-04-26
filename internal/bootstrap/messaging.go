package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// MessagingBundle returns NATS, Kafka, and RabbitMQ handlers.
func MessagingBundle() Bundle {
	return Bundle{
		Name: "messaging",
		Build: func(_ Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewNATSRequestHandler(nil),
				tooladapter.NewNATSPublishHandler(nil),
				tooladapter.NewNATSSubscribePullHandler(nil),
				tooladapter.NewKafkaConsumeHandler(nil),
				tooladapter.NewKafkaProduceHandler(nil),
				tooladapter.NewKafkaTopicMetadataHandler(nil),
				tooladapter.NewRabbitConsumeHandler(nil),
				tooladapter.NewRabbitPublishHandler(nil),
				tooladapter.NewRabbitQueueInfoHandler(nil),
				tooladapter.NewNotifyEscalationChannelHandlerFromEnv(),
			}
		},
	}
}
