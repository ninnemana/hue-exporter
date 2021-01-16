module github.com/ninnemana/hue-exporter

go 1.15

require (
	github.com/amimof/huego v1.1.0
	github.com/ninnemana/godns v0.0.0-20210108225422-08a82241641d
	github.com/prometheus/client_golang v1.7.1
	go.opentelemetry.io/otel v0.15.0
	go.opentelemetry.io/otel/exporters/metric/prometheus v0.15.0
	go.opentelemetry.io/otel/exporters/trace/jaeger v0.15.0
	go.opentelemetry.io/otel/sdk v0.15.0
	go.uber.org/zap v1.16.0
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208
)
