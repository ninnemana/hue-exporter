package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/ninnemana/hue-exporter/collector"
	"github.com/ninnemana/tracelog"

	"go.opentelemetry.io/otel/metric/global"
	"go.uber.org/zap"
)

var (
	promPort = flag.String("metric-port", "8080", "indicates the port for Prometheus metrics to be served")

	defaultPort = "8080"
)

func main() {
	flag.Parse()

	logConfig := zap.NewDevelopmentConfig()
	logConfig.Encoding = "json"

	logger, err := logConfig.Build()
	if err != nil {
		log.Fatalf("failed to create structured logger: %v", err)
	}

	defer func() {
		_ = logger.Sync()
	}()

	if promPort == nil {
		promPort = &defaultPort
	}

	flush, err := initTracer("hue")
	if err != nil {
		logger.Fatal("failed to start tracer", zap.Error(err))
	}

	defer func() {
		if err := flush(context.Background()); err != nil {
			logger.Fatal("failed to flush spans", zap.Error(err))
		}
	}()

	logger.Info("Starting metric collector")
	if err := initMeter("hue", *promPort); err != nil {
		logger.Fatal("failed to start metric server", zap.Error(err))
	}

	coll, err := collector.NewGatherer(
		collector.WithLogger(tracelog.NewLogger(tracelog.WithLogger(logger))),
		collector.WithExporter(global.GetMeterProvider()),
		collector.WithHueConfig(collector.HueConfig{
			IP:       os.Getenv("HUE_ADDRESS"),
			Username: os.Getenv("HUE_USERNAME"),
		}),
	)
	if err != nil {
		logger.Fatal("failed to create collector", zap.Error(err))
	}

	if err := coll.Run(context.Background()); err != nil {
		logger.Fatal("fell out", zap.Error(err))
	}
}
