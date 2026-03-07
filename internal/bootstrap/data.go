package bootstrap

import (
	tooladapter "github.com/underpass-ai/underpass-runtime/internal/adapters/tools"
)

// DataBundle returns Redis and MongoDB handlers.
func DataBundle() Bundle {
	return Bundle{
		Name: "data",
		Build: func(_ Config) []tooladapter.Handler {
			return []tooladapter.Handler{
				tooladapter.NewRedisGetHandler(nil),
				tooladapter.NewRedisMGetHandler(nil),
				tooladapter.NewRedisScanHandler(nil),
				tooladapter.NewRedisTTLHandler(nil),
				tooladapter.NewRedisExistsHandler(nil),
				tooladapter.NewRedisSetHandler(nil),
				tooladapter.NewRedisDelHandler(nil),
				tooladapter.NewMongoFindHandler(nil),
				tooladapter.NewMongoAggregateHandler(nil),
			}
		},
	}
}
