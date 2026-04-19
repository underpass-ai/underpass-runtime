package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricapi "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const appMeterName = "github.com/underpass-ai/underpass-runtime/internal/app"

var (
	prometheusReaderMu sync.RWMutex
	prometheusReader   *sdkmetric.ManualReader
)

func appMeter() metricapi.Meter {
	return otel.GetMeterProvider().Meter(appMeterName)
}

// MetricViews returns the OpenTelemetry metric views used by runtime metrics.
func MetricViews() []sdkmetric.View {
	return []sdkmetric.View{
		explicitHistogramView("duration_ms"),
		explicitHistogramView("workspace_invocation_quality_duration_ms"),
	}
}

// ConfigurePrometheusReader sets the manual reader used to render Prometheus
// exposition text from the OTel SDK state.
func ConfigurePrometheusReader(reader *sdkmetric.ManualReader) {
	prometheusReaderMu.Lock()
	defer prometheusReaderMu.Unlock()
	prometheusReader = reader
}

func currentPrometheusReader() *sdkmetric.ManualReader {
	prometheusReaderMu.RLock()
	defer prometheusReaderMu.RUnlock()
	return prometheusReader
}

func explicitHistogramView(name string) sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: name},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: histogramBucketBoundaries(),
				NoMinMax:   true,
			},
		},
	)
}

func histogramBucketBoundaries() []float64 {
	out := make([]float64, 0, len(invocationDurationHistogramBuckets))
	for _, bucket := range invocationDurationHistogramBuckets {
		out = append(out, float64(bucket))
	}
	return out
}

func renderPrometheusText(filter func(string) bool) string {
	reader := currentPrometheusReader()
	if reader == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		return ""
	}

	var metrics []metricdata.Metrics
	for _, scopeMetrics := range rm.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if filter != nil && !filter(metric.Name) {
				continue
			}
			metrics = append(metrics, metric)
		}
	}
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].Name < metrics[j].Name
	})

	var builder strings.Builder
	for _, metric := range metrics {
		appendPrometheusMetric(&builder, metric)
	}
	return builder.String()
}

func appendPrometheusMetric(builder *strings.Builder, metric metricdata.Metrics) {
	switch data := metric.Data.(type) {
	case metricdata.Sum[int64]:
		appendHelpAndType(builder, metric.Name, metric.Description, prometheusTypeForSum(data.IsMonotonic))
		appendSumDataPoints(builder, metric.Name, data.DataPoints)
	case metricdata.Sum[float64]:
		appendHelpAndType(builder, metric.Name, metric.Description, prometheusTypeForSum(data.IsMonotonic))
		appendSumDataPoints(builder, metric.Name, data.DataPoints)
	case metricdata.Gauge[int64]:
		appendHelpAndType(builder, metric.Name, metric.Description, "gauge")
		appendGaugeDataPoints(builder, metric.Name, data.DataPoints)
	case metricdata.Gauge[float64]:
		appendHelpAndType(builder, metric.Name, metric.Description, "gauge")
		appendGaugeDataPoints(builder, metric.Name, data.DataPoints)
	case metricdata.Histogram[int64]:
		appendHelpAndType(builder, metric.Name, metric.Description, "histogram")
		appendHistogramDataPoints(builder, metric.Name, data.DataPoints)
	case metricdata.Histogram[float64]:
		appendHelpAndType(builder, metric.Name, metric.Description, "histogram")
		appendHistogramDataPoints(builder, metric.Name, data.DataPoints)
	}
}

func appendHelpAndType(builder *strings.Builder, name, description, metricType string) {
	desc := strings.Join(strings.Fields(description), " ")
	if desc != "" {
		builder.WriteString("# HELP ")
		builder.WriteString(name)
		builder.WriteByte(' ')
		builder.WriteString(desc)
		builder.WriteByte('\n')
	}
	builder.WriteString("# TYPE ")
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(metricType)
	builder.WriteByte('\n')
}

func appendSumDataPoints[N int64 | float64](builder *strings.Builder, name string, dataPoints []metricdata.DataPoint[N]) {
	points := append([]metricdata.DataPoint[N](nil), dataPoints...)
	sort.Slice(points, func(i, j int) bool {
		return prometheusLabelBlock(points[i].Attributes) < prometheusLabelBlock(points[j].Attributes)
	})
	for _, point := range points {
		builder.WriteString(name)
		builder.WriteString(prometheusLabelBlock(point.Attributes))
		builder.WriteByte(' ')
		builder.WriteString(formatMetricNumber(point.Value))
		builder.WriteByte('\n')
	}
}

func appendGaugeDataPoints[N int64 | float64](builder *strings.Builder, name string, dataPoints []metricdata.DataPoint[N]) {
	appendSumDataPoints(builder, name, dataPoints)
}

func appendHistogramDataPoints[N int64 | float64](builder *strings.Builder, name string, dataPoints []metricdata.HistogramDataPoint[N]) {
	points := append([]metricdata.HistogramDataPoint[N](nil), dataPoints...)
	sort.Slice(points, func(i, j int) bool {
		return prometheusLabelBlock(points[i].Attributes) < prometheusLabelBlock(points[j].Attributes)
	})
	for _, point := range points {
		cumulative := uint64(0)
		for idx, upperBound := range point.Bounds {
			if idx < len(point.BucketCounts) {
				cumulative += point.BucketCounts[idx]
			}
			builder.WriteString(name)
			builder.WriteString("_bucket")
			builder.WriteString(prometheusLabelBlock(point.Attributes, attribute.String("le", strconv.FormatFloat(upperBound, 'f', -1, 64))))
			builder.WriteByte(' ')
			builder.WriteString(strconv.FormatUint(cumulative, 10))
			builder.WriteByte('\n')
		}
		if len(point.BucketCounts) > len(point.Bounds) {
			cumulative += point.BucketCounts[len(point.Bounds)]
		}
		builder.WriteString(name)
		builder.WriteString("_bucket")
		builder.WriteString(prometheusLabelBlock(point.Attributes, attribute.String("le", "+Inf")))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(cumulative, 10))
		builder.WriteByte('\n')

		builder.WriteString(name)
		builder.WriteString("_sum")
		builder.WriteString(prometheusLabelBlock(point.Attributes))
		builder.WriteByte(' ')
		builder.WriteString(formatMetricNumber(point.Sum))
		builder.WriteByte('\n')

		builder.WriteString(name)
		builder.WriteString("_count")
		builder.WriteString(prometheusLabelBlock(point.Attributes))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(point.Count, 10))
		builder.WriteByte('\n')
	}
}

func prometheusTypeForSum(monotonic bool) string {
	if monotonic {
		return "counter"
	}
	return "gauge"
}

func formatMetricNumber[N int64 | float64](value N) string {
	switch v := any(value).(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', 6, 64)
	default:
		return fmt.Sprint(v)
	}
}

func prometheusLabelBlock(attrs attribute.Set, extras ...attribute.KeyValue) string {
	labels := append([]attribute.KeyValue(nil), (&attrs).ToSlice()...)
	labels = append(labels, extras...)
	if len(labels) == 0 {
		return ""
	}
	sort.Slice(labels, func(i, j int) bool {
		leftRank := prometheusLabelRank(string(labels[i].Key))
		rightRank := prometheusLabelRank(string(labels[j].Key))
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return string(labels[i].Key) < string(labels[j].Key)
	})

	var builder strings.Builder
	builder.WriteByte('{')
	for idx, label := range labels {
		if idx > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(string(label.Key))
		builder.WriteString(`="`)
		builder.WriteString(escapePrometheusLabelValue(label.Value.Emit()))
		builder.WriteByte('"')
	}
	builder.WriteByte('}')
	return builder.String()
}

func prometheusLabelRank(key string) int {
	switch key {
	case "tool":
		return 0
	case "status", "reason", "task":
		return 1
	case "le":
		return 99
	default:
		return 10
	}
}
