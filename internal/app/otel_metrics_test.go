package app

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func installTestMetricReader(t *testing.T) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(MetricViews()...),
	)

	otel.SetMeterProvider(provider)
	ConfigurePrometheusReader(reader)

	t.Cleanup(func() {
		ConfigurePrometheusReader(nil)
		otel.SetMeterProvider(noop.NewMeterProvider())
		_ = provider.Shutdown(context.Background())
	})
}
